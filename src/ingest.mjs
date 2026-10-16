// 解码 + 入库：读抓包 NDJSON -> 切帧 -> 抽 K 线 -> 幂等写入 SQLite

import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { splitFrame, extractBars, toBar } from './frames.mjs';
import { openStore, makeWriter } from './store.mjs';

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

export function latestRawFile(root) {
  const dir = path.join(root, 'data', 'raw');
  let entries;
  try {
    entries = fs.readdirSync(dir).filter((f) => f.endsWith('.ndjson'));
  } catch {
    return null;
  }
  if (!entries.length) return null;
  const scored = entries
    .map((f) => ({ f, m: fs.statSync(path.join(dir, f)).mtimeMs }))
    .sort((a, b) => b.m - a.m);
  return path.join(dir, scored[0].f);
}

export async function ingest({ files, dbFile, log = (m) => process.stdout.write(m + '\n') }) {
  const list = (Array.isArray(files) ? files : [files]).filter(Boolean);
  if (!list.length) throw new Error('没有可处理的抓包文件');

  const db = openStore(dbFile);
  const writer = makeWriter(db);

  const summary = {
    files: list.length,
    frames: 0,
    recv: 0,
    send: 0,
    heartbeats: 0,
    json: 0,
    opaque: 0,
    bars: 0,
    byMethod: new Map(),
    resolved: new Map(),
    unknownShape: new Map(),
  };

  for (const file of list) {
    log(`读取 ${file}`);
    const hint = { symbol: null };
    const resolved = new Map();

    const rl = readline.createInterface({ input: fs.createReadStream(file), crlfDelay: Infinity });
    for await (const line of rl) {
      if (!line.trim()) continue;
      let rec;
      try {
        rec = JSON.parse(line);
      } catch {
        continue;
      }

      if (rec.k === 'meta') {
        if (rec.symbolHint) hint.symbol = rec.symbolHint;
        continue;
      }
      if (rec.k !== 'frame') continue;

      summary.frames++;
      if (rec.dir === 'recv') summary.recv++;
      else summary.send++;
      if (rec.opcode !== 1 || typeof rec.text !== 'string') {
        summary.opaque++;
        continue;
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
        if (!bars.length) {
          if (!summary.byMethod.has(method)) summary.unknownShape.set(method, 0);
          continue;
        }
        for (const entry of bars) {
          const symbol =
            resolved.get(entry.series) || resolved.get(String(entry.series)) || hint.symbol || '(unknown)';
          const bar = toBar(entry, symbol);
          if (!bar) continue;
          bar.source = method;
          writer.writeBar(bar);
          summary.bars++;
        }
      }
    }

    writer.flush();
    for (const [k, v] of resolved) summary.resolved.set(k, v);
  }

  writer.close();

  log('');
  log(`帧总数 ${summary.frames}（收 ${summary.recv} / 发 ${summary.send}）`);
  log(`心跳 ~h~ ${summary.heartbeats} 段 · JSON 段落 ${summary.json} · 非文本/无法解析 ${summary.opaque}`);
  log(`写入 K 线 ${summary.bars} 根（按 symbol+series+t 幂等）`);
  if (summary.resolved.size) {
    log(`series → symbol 映射：${[...summary.resolved].map(([k, v]) => `${k}=${v}`).join(', ')}`);
  }
  const top = [...summary.byMethod].sort((a, b) => b[1] - a[1]).slice(0, 15);
  if (top.length) {
    log('');
    log('出现频次最高的方法：');
    for (const [m, n] of top) log(`  ${String(n).padStart(6)}  ${m}`);
  }
  return summary;
}
