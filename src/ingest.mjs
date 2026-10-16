// 解码 + 入库：读抓包 NDJSON -> 切帧 -> 抽 K 线 -> 幂等写入对应品种的 SQLite
//
// 两种用法共用同一套解码逻辑，避免"离线能解、在线解不了"的分叉：
//
//   createIngestor({ root }).push(rec)   流式：常驻会话边抓边入库
//   runIngest({ root, files })           批处理：事后扫历史抓包文件
//
// 品种路由：每根 K 线写进它自己品种的库文件（data/db/<slug>.sqlite）。
// 品种名的来源按可靠性排序：
//   1. 帧里若出现 symbol_resolved 之类的消息（实测 prodata 没有，但留着）
//   2. 抓包文件自己的 meta.symbolHint，或 raw 索引里记的品种
//   3. 都没有 -> data/db/unknown.sqlite
//
// 幂等性由 store 的 (symbol, series, t) 主键保证：同一根 K 线抓到几次都只留一份，
// 这正好让"回放反复来回跳、窗口大面积重叠"变成无害操作 —— 重叠部分自动去重。

import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { splitFrame, extractBars, toBar } from './frames.mjs';
import { openSymbolStore, makeWriter } from './store.mjs';
import { UNKNOWN_SYMBOL } from './registry.mjs';
import { listRawFiles, latestRawFile as latestRaw } from './rawindex.mjs';

export { listRawFiles };
export const latestRawFile = latestRaw;

export const LEDGER_FILE = (root) => path.join(root, 'data', 'ingest-state.json');

export function readLedger(root) {
  try {
    return JSON.parse(fs.readFileSync(LEDGER_FILE(root), 'utf8'));
  } catch {
    return {};
  }
}

export function writeLedger(root, obj) {
  const f = LEDGER_FILE(root);
  fs.mkdirSync(path.dirname(f), { recursive: true });
  fs.writeFileSync(f, JSON.stringify(obj, null, 2) + '\n');
  return obj;
}

// 抓包里如果出现 symbol_resolved 一类的消息，就顺手建立 series -> 品种名 的映射。
// 实测（2026-10-16）prodata 抓包里并没有这种消息，品种只能从页面 URL 的 ?symbol= 兜底，
// 但这段留着不亏：真出现了就用更权威的映射。
function collectResolved(node, map) {
  if (Array.isArray(node)) {
    for (const n of node) collectResolved(n, map);
    return map;
  }
  if (!node || typeof node !== 'object') return map;
  const sym = typeof node.symbol === 'string' ? node.symbol : null;
  const sid = [node.series_id, node.seriesId, node.sds].find((x) => typeof x === 'string');
  if (sym && sid) map.set(sid, sym);
  for (const v of Object.values(node)) collectResolved(v, map);
  return map;
}

// 多品种写手：按 symbol 懒加载每个库的 writer，同时最多保持 maxOpen 个连接。
// 常驻会话会跑很久，连接数得有个上限，否则回放几十个品种后句柄会失控。
export function createSymbolRouter({ root, maxOpen = 8 }) {
  const open = new Map(); // symbol -> { db, writer, file, lastUsed }
  const counters = new Map(); // symbol -> { bars, added }

  function bump(symbol, n = 1) {
    let c = counters.get(symbol);
    if (!c) {
      c = { bars: 0, added: 0 };
      counters.set(symbol, c);
    }
    c.bars += n;
    return c;
  }

  function evictIfNeeded() {
    while (open.size > maxOpen) {
      let oldest = null;
      for (const [sym, rec] of open) {
        if (!oldest || rec.lastUsed < oldest[1].lastUsed) oldest = [sym, rec];
      }
      if (!oldest) break;
      try {
        oldest[1].writer.close();
      } catch {}
      open.delete(oldest[0]);
    }
  }

  function forSymbol(symbol) {
    const sym = symbol || UNKNOWN_SYMBOL;
    let rec = open.get(sym);
    if (!rec) {
      const { db, file } = openSymbolStore(root, sym);
      // writer 绑定品种：库里的 symbol 列不会再出现别的值
      rec = { db, writer: makeWriter(db, { symbol: sym }), file, lastUsed: Date.now() };
      open.set(sym, rec);
      evictIfNeeded();
    }
    rec.lastUsed = Date.now();
    return rec;
  }

  return {
    counters,
    forSymbol,
    openFiles: () => [...open].map(([symbol, r]) => ({ symbol, file: r.file })),
    flushAll() {
      for (const r of open.values()) {
        try {
          r.writer.flush();
        } catch {}
      }
    },
    closeAll() {
      for (const r of open.values()) {
        try {
          r.writer.close();
        } catch {}
      }
      open.clear();
    },
    bump,
  };
}

export function createIngestor({ root, symbol = null, maxOpen = 8, log = () => {} } = {}) {
  const router = createSymbolRouter({ root, maxOpen });
  const resolved = new Map();
  const symbolHint = { symbol: null };
  const summary = {
    frames: 0,
    recv: 0,
    send: 0,
    heartbeats: 0,
    json: 0,
    opaque: 0,
    bars: 0,
    skipped: 0,
    byMethod: new Map(),
    bySymbol: new Map(),
    resolved,
  };

  function noteSymbol(sym, n) {
    let c = summary.bySymbol.get(sym);
    if (!c) {
      c = { bars: 0 };
      summary.bySymbol.set(sym, c);
    }
    c.bars += n;
  }

  function push(rec) {
    if (!rec || typeof rec !== 'object') return;

    if (rec.k === 'meta') {
      if (rec.symbolHint && !symbolHint.symbol) symbolHint.symbol = rec.symbolHint;
      return;
    }
    if (rec.k !== 'frame') return;

    summary.frames++;
    if (rec.dir === 'recv') summary.recv++;
    else summary.send++;

    // 只处理文本帧。二进制帧在抓包时已按 base64 保真存下，
    // 但实测 prodata 全是文本（910 段 JSON 零失败），所以这里直接跳过并计数。
    if (rec.opcode !== 1 || typeof rec.text !== 'string') {
      summary.opaque++;
      return;
    }

    for (const seg of splitFrame(rec.text)) {
      if (seg.kind === 'heartbeat') {
        summary.heartbeats++;
        continue;
      }
      if (seg.kind !== 'json') {
        summary.opaque++;
        continue;
      }
      summary.json++;
      const method = seg.value?.m ?? '(no-method)';
      summary.byMethod.set(method, (summary.byMethod.get(method) || 0) + 1);

      collectResolved(seg.value, resolved);

      const bars = extractBars(seg.value);
      if (!bars.length) continue;

      for (const entry of bars) {
        const sym =
          resolved.get(entry.series) ||
          resolved.get(String(entry.series)) ||
          symbolHint.symbol ||
          UNKNOWN_SYMBOL;
        if (symbol && sym !== symbol) {
          summary.skipped++;
          continue;
        }
        const bar = toBar(entry, sym);
        if (!bar) continue;
        bar.source = method;

        const target = router.forSymbol(sym);
        target.writer.writeBar(bar);
        summary.bars++;
        noteSymbol(sym, 1);
      }
    }
  }

  return {
    push,
    flush: () => router.flushAll(),
    root,
    summary,
    symbolHint,
    resolved,
    openFiles: router.openFiles,
    counters: router.counters,
    // 暴露给常驻会话：它要拿同一个写连接去查"现在这个品种有多少根"，
    // 另开连接也不是不行，但共用能保证一定看得到刚才写进去的行。
    forSymbol: router.forSymbol,
    close() {
      router.closeAll();
    },
  };
}

// 批处理入口：把若干抓包文件解码入库
export async function runIngest({
  root,
  files,
  symbol = null,
  log = (m) => process.stdout.write(m + '\n'),
}) {
  const list = (Array.isArray(files) ? files : [files]).filter(Boolean);
  if (!list.length) throw new Error('没有可处理的抓包文件');

  const ing = createIngestor({ root, symbol, log });
  const perFile = [];

  for (const item of list) {
    const file = typeof item === 'string' ? item : item.file;
    const fileSymbol = typeof item === 'string' ? null : item.symbol || null;
    const before = ing.summary.bars;
    const rl = readline.createInterface({ input: fs.createReadStream(file), crlfDelay: Infinity });
    let lines = 0;
    for await (const line of rl) {
      if (!line.trim()) continue;
      lines++;
      let rec;
      try {
        rec = JSON.parse(line);
      } catch {
        continue;
      }
      // 文件级品种提示：帧里的 meta 只出现在第一条，但万一文件被截断过，
      // raw 索引里记的品种仍然可用
      if (fileSymbol && rec.k === 'meta' && !rec.symbolHint) rec.symbolHint = fileSymbol;
      ing.push(rec);
    }
    ing.flush();
    let st = { size: 0, mtimeMs: 0 };
    try {
      st = fs.statSync(file);
    } catch {}
    const added = ing.summary.bars - before;
    log(`读取 ${path.basename(file)} · ${lines} 条记录 · ${added} 根`);
    perFile.push({
      file,
      name: path.basename(file),
      lines,
      bars: added,
      size: st.size,
      mtimeMs: st.mtimeMs,
      symbol: fileSymbol,
    });
  }

  const bySymbol = Object.fromEntries(
    [...ing.summary.bySymbol].map(([k, v]) => [k, v.bars]),
  );
  const openFiles = ing.openFiles();
  ing.close();
  return { summary: ing.summary, files: perFile, bySymbol, dbFiles: openFiles };
}

// 增量扫描：默认只处理新增或变动过的文件（台账 data/ingest-state.json）
export function pickChangedFiles(root, { symbol = null, force = false, ledger = null } = {}) {
  const all = listRawFiles(root, { symbol }).filter((f) => f.size > 0);
  const led = ledger || readLedger(root);
  if (force) return { files: all, ledger: led, all };
  const changed = all.filter((f) => {
    const prev = led[f.name];
    return !prev || prev.size !== f.size || prev.mtimeMs !== f.mtimeMs;
  });
  return { files: changed, ledger: led, all };
}

export function formatSummary(s, log = (m) => process.stdout.write(m + '\n')) {
  log('');
  log(`帧总数 ${s.frames}（收 ${s.recv} / 发 ${s.send}）`);
  log(`心跳 ~h~ ${s.heartbeats} 段 · JSON 段落 ${s.json} · 非文本/无法解析 ${s.opaque}`);
  log(
    `写入 K 线 ${s.bars} 根（按 symbol+series+t 幂等）` +
      (s.skipped ? ` · 因品种过滤跳过 ${s.skipped} 根` : ''),
  );
  if (s.bySymbol.size) {
    log('分品种：');
    for (const [sym, v] of [...s.bySymbol].sort((a, b) => b[1].bars - a[1].bars)) {
      log(`  ${String(v.bars).padStart(6)}  ${sym}`);
    }
  }
  if (s.resolved.size) {
    log(`series → symbol 映射：${[...s.resolved].map(([k, v]) => `${k}=${v}`).join(', ')}`);
  }
  const top = [...s.byMethod].sort((a, b) => b[1] - a[1]).slice(0, 10);
  if (top.length) {
    log('');
    log('出现频次最高的方法：');
    for (const [m, n] of top) log(`  ${String(n).padStart(6)}  ${m}`);
  }
}
