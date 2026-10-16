// 原始抓包文件的索引
//
// 抓包文件本身不知道自己属于哪个品种 —— 帧里没有 symbol_resolved（实测确认），
// 品种只能从图表页 URL 的 ?symbol= 推出来。所以 capture 落盘时顺手把"这个文件
// 是哪个品种的"记在这里，事后入库就不用再猜。
//
// 顺带解决另一件事：文件按品种分目录（data/raw/<slug>/），
// 这样"只入库某个品种"是目录级操作，不需要先扫全部文件再过滤。

import fs from 'node:fs';
import path from 'node:path';
import { symbolSlug } from './registry.mjs';

export function rawRoot(root) {
  return path.join(root, 'data', 'raw');
}

export function rawIndexFile(root) {
  return path.join(rawRoot(root), 'index.json');
}

export function readRawIndex(root) {
  try {
    const raw = JSON.parse(fs.readFileSync(rawIndexFile(root), 'utf8'));
    if (raw && typeof raw === 'object') return raw;
  } catch {}
  return { version: 1, files: {} };
}

export function writeRawIndex(root, idx) {
  const dir = rawRoot(root);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, `.index.json.tmp-${process.pid}`);
  fs.writeFileSync(tmp, JSON.stringify(idx, null, 2) + '\n');
  fs.renameSync(tmp, rawIndexFile(root));
  return idx;
}

export function rememberRawFile(root, { file, symbol, url, title }) {
  const idx = readRawIndex(root);
  idx.files[path.basename(file)] = {
    symbol: symbol || null,
    url: url || null,
    title: title || null,
    at: Date.now(),
  };
  writeRawIndex(root, idx);
}

// 抓包文件的默认存放目录：知道品种就分目录，不知道就放根目录
export function rawDirFor(root, symbol) {
  return symbol ? path.join(rawRoot(root), symbolSlug(symbol)) : rawRoot(root);
}

// 列出抓包文件（可按品种过滤）。
// 同时扫根目录和品种子目录 —— 根目录里躺着的是重构之前抓的，不该被忽略。
export function listRawFiles(root, { symbol = null } = {}) {
  const base = rawRoot(root);
  const idx = readRawIndex(root);
  const out = [];

  const pushDir = (dir, dirSymbol) => {
    let names;
    try {
      names = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const ent of names) {
      if (!ent.isFile() || !ent.name.endsWith('.ndjson')) continue;
      const file = path.join(dir, ent.name);
      let st;
      try {
        st = fs.statSync(file);
      } catch {
        continue;
      }
      const meta = idx.files[ent.name] || {};
      out.push({
        file,
        name: ent.name,
        size: st.size,
        mtimeMs: st.mtimeMs,
        symbol: meta.symbol || dirSymbol || null,
        url: meta.url || null,
        title: meta.title || null,
        inSymbolDir: Boolean(dirSymbol),
      });
    }
  };

  pushDir(path.join(base, ''), null);
  let subdirs = [];
  try {
    subdirs = fs.readdirSync(base, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name);
  } catch {
    subdirs = [];
  }
  for (const sub of subdirs) {
    pushDir(path.join(base, sub), sub);
  }

  const filtered = symbol ? out.filter((f) => f.symbol === symbol) : out;
  return filtered.sort((a, b) => a.mtimeMs - b.mtimeMs);
}

export function latestRawFile(root, opts = {}) {
  const all = listRawFiles(root, opts);
  return all.length ? all[all.length - 1].file : null;
}

export function emptyRawFiles(root) {
  return listRawFiles(root).filter((f) => f.size === 0);
}

// 给"索引里还没有"的抓包文件回填品种：读每个文件的第一行。
// capture 一定把 meta 记录写在最前面，里面带 symbolHint（从图表页 URL 的 ?symbol= 推出来）。
// 只读首行是为了不把几十 MB 的文件整个读进来。
export function reconcileRawIndex(root, { limit = 500 } = {}) {
  const idx = readRawIndex(root);
  const files = listRawFiles(root).filter((f) => !idx.files[f.name] && f.size > 0).slice(0, limit);
  if (!files.length) return { updated: 0, scanned: 0 };

  let updated = 0;
  for (const f of files) {
    const head = readFirstLines(f.file, 1);
    for (const line of head) {
      let rec;
      try {
        rec = JSON.parse(line);
      } catch {
        continue;
      }
      if (rec?.k === 'meta') {
        idx.files[f.name] = {
          symbol: rec.symbolHint || f.symbol || null,
          url: rec.target?.url || null,
          title: rec.target?.title || null,
          at: Number(rec.at) || f.mtimeMs,
          backfilled: true,
        };
        updated++;
        break;
      }
    }
  }
  if (updated) writeRawIndex(root, idx);
  return { updated, scanned: files.length };
}

// 只读前 N 行，读完立刻关流，不把整个文件读进内存
function readFirstLines(file, count) {
  const out = [];
  try {
    const fd = fs.openSync(file, 'r');
    try {
      const buf = Buffer.alloc(64 * 1024);
      let carry = '';
      let read = 0;
      while (out.length < count && (read = fs.readSync(fd, buf, 0, buf.length, null)) > 0) {
        carry += buf.toString('utf8', 0, read);
        let nl;
        while (out.length < count && (nl = carry.indexOf('\n')) >= 0) {
          out.push(carry.slice(0, nl));
          carry = carry.slice(nl + 1);
        }
        if (carry.length > 4 * 1024 * 1024) break;
      }
      if (out.length < count && carry.trim()) out.push(carry);
    } finally {
      fs.closeSync(fd);
    }
  } catch {}
  return out;
}
