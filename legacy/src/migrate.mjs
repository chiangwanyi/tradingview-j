// 一次性迁移：把旧的"全品种混在一个文件"的库拆成每品种一个文件
//
// data/bars.sqlite  ->  data/db/<slug>.sqlite（每个 symbol 一个）
//
// 原则：
//   - 只读旧库，绝不删、绝不改。迁移完旧文件原地留着，确认新库没问题了自己删。
//   - 幂等：靠 data/db/.migrated-from-legacy.json 记住旧库的 size/mtime，
//     没变过就不再跑；旧库又长了（比如迁移后还用过老 CLI）也会重新补一次。
//   - 一个品种失败不影响其他品种。

import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import {
  dbDir,
  fileForSymbol,
  legacyDbFile,
  migrationMarker,
  saveIndex,
  loadIndex,
} from './registry.mjs';
import { openSymbolStore, openStore } from './store.mjs';

function readMarker(root) {
  try {
    const m = JSON.parse(fs.readFileSync(migrationMarker(root), 'utf8'));
    return m && typeof m === 'object' ? m : null;
  } catch {
    return null;
  }
}

function writeMarker(root, data) {
  fs.mkdirSync(dbDir(root), { recursive: true });
  fs.writeFileSync(migrationMarker(root), JSON.stringify(data, null, 2) + '\n');
}

export function legacyStatus(root) {
  const file = legacyDbFile(root);
  let st = null;
  try {
    st = fs.statSync(file);
  } catch {
    return { exists: false, file };
  }
  const marker = readMarker(root);
  const unchanged = Boolean(
    marker && marker.legacySize === st.size && marker.legacyMtimeMs === st.mtimeMs,
  );
  return { exists: true, file, size: st.size, mtimeMs: st.mtimeMs, marker, unchanged };
}

export function migrateLegacy(root, { force = false, log = () => {} } = {}) {
  const status = legacyStatus(root);
  if (!status.exists) return { migrated: false, reason: 'no-legacy', symbols: [] };
  if (status.unchanged && !force) {
    return { migrated: false, reason: 'already-done', symbols: status.marker?.symbols || [] };
  }

  // 只读打开，避免迁移过程意外写入旧库
  const src = new DatabaseSync(status.file, { readOnly: true });
  let symbols = [];
  try {
    symbols = src
      .prepare(`SELECT symbol, COUNT(*) AS rows, MIN(t) AS a, MAX(t) AS b FROM bars GROUP BY symbol ORDER BY symbol`)
      .all();
  } catch {
    src.close();
    return { migrated: false, reason: 'legacy-has-no-bars-table', symbols: [] };
  }
  if (!symbols.length) {
    src.close();
    writeMarker(root, { at: Date.now(), legacySize: status.size, legacyMtimeMs: status.mtimeMs, symbols: [] });
    return { migrated: false, reason: 'legacy-empty', symbols: [] };
  }

  log(`迁移旧库：${status.file}`);
  log(`  发现 ${symbols.length} 个品种：${symbols.map((s) => `${s.symbol}(${s.rows})`).join(', ')}`);

  const idx = loadIndex(root);
  const done = [];
  const failed = [];
  for (const s of symbols) {
    const sym = s.symbol;
    try {
      // 先登记索引，文件名稳定
      fileForSymbol(root, sym);
      const { db: dst, file } = openSymbolStore(root, sym);

      const rows = src
        .prepare(
          `SELECT symbol, series, t, interval, timescale, o, h, l, c, v, source, raw, seen_at
           FROM bars WHERE symbol = ?`,
        )
        .all(sym);

      const before = dst.prepare(`SELECT COUNT(*) AS n FROM bars WHERE symbol = ?`).get(sym).n;

      dst.exec('BEGIN');
      try {
        const ins = dst.prepare(`
          INSERT INTO bars (symbol, series, t, interval, timescale, o, h, l, c, v, source, raw, seen_at)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
          ON CONFLICT(symbol, series, t) DO UPDATE SET
            o=excluded.o, h=excluded.h, l=excluded.l, c=excluded.c, v=excluded.v,
            interval=COALESCE(excluded.interval, bars.interval),
            timescale=COALESCE(excluded.timescale, bars.timescale),
            raw=excluded.raw, seen_at=excluded.seen_at
        `);
        for (const r of rows) {
          ins.run(
            r.symbol, r.series, r.t, r.interval ?? null, r.timescale ?? null,
            r.o, r.h, r.l, r.c, r.v, r.source ?? null, r.raw ?? null, r.seen_at ?? Date.now(),
          );
        }
        dst.exec('COMMIT');
      } catch (e) {
        try { dst.exec('ROLLBACK'); } catch {}
        throw e;
      }

      const after = dst.prepare(`SELECT COUNT(*) AS n FROM bars WHERE symbol = ?`).get(sym).n;
      dst.close();
      log(`  ${sym} -> ${path.basename(file)}  旧库 ${rows.length} 行 · 目标库 ${before} -> ${after} 行`);
      done.push({ symbol: sym, file, legacyRows: rows.length, before, after });
    } catch (e) {
      failed.push({ symbol: sym, error: e.message });
      log(`  ✗ ${sym} 迁移失败：${e.message}`);
    }
  }

  src.close();
  // 别拿循环开始前读到的 idx 直接写回去 —— fileForSymbol() 在循环里各自
  // 读-改-写了 index.json，用旧快照覆盖会把刚登记好的映射抹掉（踩过一次）。
  // 正确做法：重新读盘上的最新索引，只补上还缺的条目。
  const fresh = loadIndex(root);
  for (const d of done) {
    if (!fresh.symbols[d.symbol]) {
      fresh.symbols[d.symbol] = {
        file: path.basename(d.file),
        slug: path.basename(d.file).replace(/\.sqlite$/, ''),
        created: Date.now(),
      };
    }
  }
  saveIndex(root, fresh);
  writeMarker(root, {
    at: Date.now(),
    legacySize: status.size,
    legacyMtimeMs: status.mtimeMs,
    legacyFile: status.file,
    symbols: done.map((d) => d.symbol),
    failed,
  });

  log('');
  log(`迁移完成：${done.length} 个品种${failed.length ? `，${failed.length} 个失败` : ''}。`);
  log('旧库 data/bars.sqlite 原样保留（没有被修改也没有被删除），确认无误后可以自行删除。');
  return { migrated: true, reason: 'ok', symbols: done.map((d) => d.symbol), done, failed };
}
