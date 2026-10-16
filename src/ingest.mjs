// 解码 + 入库：读抓包 NDJSON -> 切帧 -> 抽 K 线 -> 幂等写入 SQLite
//
// 两种用法共用同一套解码逻辑，避免"离线能解、在线解不了"的分叉：
//
//   createIngestor({ dbFile }).push(rec)   流式：watch 模式边抓边入库
//   ingest({ files, dbFile })              批处理：事后扫历史抓包文件
//
// 幂等性由 store 的 (symbol, series, t) 主键保证：同一根 K 线抓到几次都只留一份，
// 这正好让"回放反复来回跳、窗口大面积重叠"变成无害操作 —— 重叠部分自动去重。

import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { splitFrame, extractBars, toBar } from './frames.mjs';
import { openStore, makeWriter } from './store.mjs';

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

export function rawDirOf(root) {
  return path.join(root, 'data', 'raw');
}

// 按修改时间升序返回抓包文件，带上 size/mtime 供增量入库判断
export function listRawFiles(root) {
  const dir = rawDirOf(root);
  let entries;
  try {
    entries = fs.readdirSync(dir).filter((f) => f.endsWith('.ndjson'));
  } catch {
    return [];
  }
  return entries
    .map((f) => {
      const file = path.join(dir, f);
      const st = fs.statSync(file);
      return { file, name: f, mtimeMs: st.mtimeMs, size: st.size };
    })
    .sort((a, b) => a.mtimeMs - b.mtimeMs);
}

export function latestRawFile(root) {
  const all = listRawFiles(root);
  return all.length ? all[all.length - 1].file : null;
}

// 流式解码器：喂 NDJSON 记录进去，K 线进库
export function createIngestor({ dbFile, symbol = null, log = () => {} } = {}) {
  const db = openStore(dbFile);
  const writer = makeWriter(db);
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
    resolved,
  };

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
          '(unknown)';
        if (symbol && sym !== symbol) {
          summary.skipped++;
          continue;
        }
        const bar = toBar(entry, sym);
        if (!bar) continue;
        bar.source = method;
        writer.writeBar(bar);
        summary.bars++;
      }
    }
  }

  return {
    push,
    flush: () => writer.flush(),
    db,
    summary,
    symbolHint,
    resolved,
    close() {
      // 注意：store 的 makeWriter.close() 内部已经 flush 并关闭了 db，
      // 这里不能再 db.close() 一次 —— 重复关闭会抛 ERR_INVALID_STATE。
      writer.close();
    },
  };
}

export function formatSummary(s, log = (m) => process.stdout.write(m + '\n')) {
  log('');
  log(`帧总数 ${s.frames}（收 ${s.recv} / 发 ${s.send}）`);
  log(`心跳 ~h~ ${s.heartbeats} 段 · JSON 段落 ${s.json} · 非文本/无法解析 ${s.opaque}`);
  log(
    `写入 K 线 ${s.bars} 根（按 symbol+series+t 幂等）` +
      (s.skipped ? ` · 因 --symbol 过滤跳过 ${s.skipped} 根` : ''),
  );
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

export async function ingest({ files, dbFile, symbol = null, log = (m) => process.stdout.write(m + '\n') }) {
  const list = (Array.isArray(files) ? files : [files]).filter(Boolean);
  if (!list.length) throw new Error('没有可处理的抓包文件');

  const ing = createIngestor({ dbFile, symbol, log });
  const perFile = [];

  for (const file of list) {
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
      ing.push(rec);
    }
    ing.flush();
    const st = fs.statSync(file);
    const added = ing.summary.bars - before;
    log(`读取 ${path.basename(file)} · ${lines} 条记录 · ${added} 根`);
    perFile.push({ file, name: path.basename(file), lines, bars: added, size: st.size, mtimeMs: st.mtimeMs });
  }

  ing.close();
  return { summary: ing.summary, files: perFile };
}
