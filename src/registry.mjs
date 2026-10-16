// 品种 -> 独立 sqlite 文件的映射层
//
// 为什么一个品种一个库（而不是全塞进 data/bars.sqlite 靠 symbol 列区分）：
//   1. 不同品种的周期和交易时段完全不同 —— 混在一起的话，覆盖度报告和导出
//      每次都要先按 symbol 过滤，稍不注意就把两个品种的 K 线当成一条时间轴来算。
//   2. 单文件损坏只影响一个品种，不会把全部数据一起带走。
//   3. 备份/分享单个品种的数据就是拷贝一个文件的事。
//
// 文件名用 slug（把 Windows 不允许的字符换掉），真实品种名存在每个库自己的
// meta 表里 —— slug 只负责"能落盘"，不负责"可读"。
//
// Windows 上还有个坑：文件系统大小写不敏感，两个只差大小写的品种会撞到同一个
// 文件。所以这里维护一份显式索引（data/db/index.json），symbol 到文件名一一对应，
// 撞名时加序号，绝不靠"猜"。

import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

export const UNKNOWN_SYMBOL = '(unknown)';

export function dbDir(root) {
  return path.join(root, 'data', 'db');
}

export function indexFile(root) {
  return path.join(dbDir(root), 'index.json');
}

// CME_MINI:MNQ1!  ->  CME_MINI_MNQ1!
export function symbolSlug(symbol) {
  const s = String(symbol ?? UNKNOWN_SYMBOL)
    // Windows 非法字符 + 控制字符
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, '_')
    // Windows 不允许文件名以点或空格结尾
    .replace(/[. ]+$/, '')
    .trim();
  return s || 'unknown';
}

function shortHash(s) {
  return crypto.createHash('sha1').update(String(s)).digest('hex').slice(0, 6);
}

export function loadIndex(root) {
  try {
    const raw = JSON.parse(fs.readFileSync(indexFile(root), 'utf8'));
    if (raw && typeof raw === 'object' && raw.symbols && typeof raw.symbols === 'object') return raw;
  } catch {}
  return { version: 1, symbols: {} };
}

export function saveIndex(root, idx) {
  const dir = dbDir(root);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, `.index.json.tmp-${process.pid}`);
  fs.writeFileSync(tmp, JSON.stringify(idx, null, 2) + '\n');
  fs.renameSync(tmp, indexFile(root));
  return idx;
}

// 返回某个品种应当使用的库文件路径；索引里没有就分配一个并落盘。
// 分配规则：slug 优先；若 slug 已被别的品种占用（大小写或字符归一后撞车），
// 追加一个短哈希，保证稳定且互不覆盖。
export function fileForSymbol(root, symbol, { persist = true } = {}) {
  const sym = String(symbol || UNKNOWN_SYMBOL);
  const idx = loadIndex(root);
  const existing = idx.symbols[sym];
  if (existing?.file) return path.join(dbDir(root), existing.file);

  const base = symbolSlug(sym);
  const taken = new Set(Object.values(idx.symbols).map((v) => String(v.file).toLowerCase()));
  let file = `${base}.sqlite`;
  if (taken.has(file.toLowerCase())) file = `${base}~${shortHash(sym)}.sqlite`;
  let n = 2;
  while (taken.has(file.toLowerCase())) file = `${base}~${shortHash(sym)}-${n++}.sqlite`;

  idx.symbols[sym] = { file, slug: file.replace(/\.sqlite$/, ''), created: Date.now() };
  if (persist) saveIndex(root, idx);
  return path.join(dbDir(root), file);
}

export function slugForSymbol(root, symbol) {
  const idx = loadIndex(root);
  const rec = idx.symbols[String(symbol)];
  return rec ? rec.slug : symbolSlug(symbol);
}

// slug -> symbol，从索引反查。索引里没有就返回 null（不猜）。
export function symbolForSlug(root, slug) {
  const idx = loadIndex(root);
  for (const [sym, rec] of Object.entries(idx.symbols)) {
    if (rec.slug === slug || String(rec.file).replace(/\.sqlite$/, '') === slug) return sym;
  }
  return null;
}

// 列出所有品种库。磁盘上存在但索引里没有的文件也会被认出来
// （用户手工拷进来的库不该被忽略），但不会被写回索引 —— 只有真正用到时才登记。
export function listDbFiles(root) {
  const dir = dbDir(root);
  let files;
  try {
    files = fs.readdirSync(dir).filter((f) => f.endsWith('.sqlite'));
  } catch {
    return [];
  }
  const idx = loadIndex(root);
  const byFile = new Map();
  for (const [sym, rec] of Object.entries(idx.symbols)) byFile.set(String(rec.file), sym);

  const out = [];
  for (const f of files) {
    const slug = f.replace(/\.sqlite$/, '');
    out.push({
      file: path.join(dir, f),
      filename: f,
      slug,
      symbol: byFile.get(f) || null,
      indexed: byFile.has(f),
    });
  }
  // 索引里有、磁盘上没有的：留下痕迹，方便诊断"文件被移走了"
  for (const [sym, rec] of Object.entries(idx.symbols)) {
    if (!out.some((x) => x.filename === rec.file)) {
      out.push({
        file: path.join(dir, rec.file),
        filename: rec.file,
        slug: rec.slug,
        symbol: sym,
        indexed: true,
        missing: true,
      });
    }
  }
  return out.sort((a, b) => (a.symbol || a.slug).localeCompare(b.symbol || b.slug));
}

// 旧的"全部混在一个文件"的库
export function legacyDbFile(root) {
  return path.join(root, 'data', 'bars.sqlite');
}

export function migrationMarker(root) {
  return path.join(dbDir(root), '.migrated-from-legacy.json');
}
