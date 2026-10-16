// 落库：按 (symbol, series, t) 幂等，重复抓到同一根 K 线就覆盖
// 这样 du 增量更新正在走的那根 K 线时，库里始终是最后一次收到的版本。

import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { intervalName } from './frames.mjs';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS bars (
  symbol   TEXT NOT NULL,
  series   TEXT NOT NULL,
  t        INTEGER NOT NULL,
  interval TEXT,
  timescale TEXT,
  o REAL NOT NULL,
  h REAL NOT NULL,
  l REAL NOT NULL,
  c REAL NOT NULL,
  v REAL,
  source   TEXT,
  raw      TEXT,
  seen_at  INTEGER NOT NULL,
  PRIMARY KEY (symbol, series, t)
);
CREATE INDEX IF NOT EXISTS idx_bars_symbol_t ON bars (symbol, t);

CREATE TABLE IF NOT EXISTS series_meta (
  symbol    TEXT NOT NULL,
  series    TEXT NOT NULL,
  interval  TEXT,
  first_t   INTEGER,
  last_t    INTEGER,
  bars      INTEGER DEFAULT 0,
  updated_at INTEGER,
  PRIMARY KEY (symbol, series)
);

CREATE TABLE IF NOT EXISTS runs (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  file     TEXT,
  target   TEXT,
  started  INTEGER,
  finished INTEGER,
  recv     INTEGER DEFAULT 0,
  send     INTEGER DEFAULT 0,
  bars     INTEGER DEFAULT 0
);
`;

// 从相邻时间戳的众数差反推周期 —— 兜底用。
// 首选是帧里自带的 lbs.bar_close_time（t 到 收盘时间 的差就是周期），
// 只有拿不到时才靠 K 线自己说话。
export function inferInterval(db, symbol, series) {
  const explicit = db
    .prepare(
      `SELECT interval FROM bars
       WHERE symbol = ? AND series = ? AND interval IS NOT NULL
       ORDER BY t DESC LIMIT 1`,
    )
    .get(symbol, series);
  if (explicit?.interval) return explicit.interval;

  const ts = db
    .prepare(`SELECT t FROM bars WHERE symbol = ? AND series = ? ORDER BY t LIMIT 3000`)
    .all(symbol, series)
    .map((r) => r.t);
  if (ts.length < 3) return null;
  const counts = new Map();
  for (let i = 1; i < ts.length; i++) {
    const d = ts[i] - ts[i - 1];
    if (d <= 0) continue;
    counts.set(d, (counts.get(d) || 0) + 1);
  }
  let best = null;
  for (const [d, n] of counts) if (!best || n > best[1]) best = [d, n];
  if (!best) return null;
  return intervalName(best[0]);
}

export function openStore(file) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL');
  db.exec('PRAGMA synchronous = NORMAL');
  db.exec(SCHEMA);
  return db;
}

export function makeWriter(db) {
  // 极简事务：攒够 N 条或隔一段时间提交一次，避免每根 K 线都刷盘
  let pending = 0;
  const upsert = db.prepare(`
    INSERT INTO bars (symbol, series, t, interval, timescale, o, h, l, c, v, source, raw, seen_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(symbol, series, t) DO UPDATE SET
      o = excluded.o, h = excluded.h, l = excluded.l, c = excluded.c,
      v = excluded.v, interval = COALESCE(excluded.interval, bars.interval),
      timescale = COALESCE(excluded.timescale, bars.timescale),
      raw = excluded.raw, seen_at = excluded.seen_at
  `);
  const markInterval = db.prepare(
    `UPDATE bars SET interval = ? WHERE symbol = ? AND series = ? AND interval IS NULL`,
  );
  const rollup = db.prepare(`
    INSERT INTO series_meta (symbol, series, interval, first_t, last_t, bars, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(symbol, series) DO UPDATE SET
      interval = COALESCE(excluded.interval, series_meta.interval),
      first_t = MIN(COALESCE(series_meta.first_t, excluded.first_t), excluded.first_t),
      last_t  = MAX(COALESCE(series_meta.last_t, excluded.last_t), excluded.last_t),
      bars    = excluded.bars,
      updated_at = excluded.updated_at
  `);

  const touched = new Set();
  const now = () => Date.now();

  return {
    writeBar(bar) {
      if (!bar.symbol) bar.symbol = '(unknown)';
      upsert.run(
        bar.symbol,
        String(bar.series ?? '(unknown)'),
        bar.t,
        bar.interval ?? null,
        bar.timescale ?? null,
        bar.o,
        bar.h,
        bar.l,
        bar.c,
        bar.v,
        bar.source ?? null,
        JSON.stringify(bar.raw ?? null),
        now(),
      );
      touched.add(`${bar.symbol}\u0000${bar.series ?? '(unknown)'}`);
      if (++pending >= 500) this.flush();
    },
    flush() {
      for (const key of touched) {
        const [symbol, series] = key.split('\u0000');
        const row = db
          .prepare(
            `SELECT COUNT(*) AS bars, MIN(t) AS first_t, MAX(t) AS last_t
             FROM bars WHERE symbol = ? AND series = ?`,
          )
          .get(symbol, series);
        if (!row) continue;
        const interval = inferInterval(db, symbol, series);
        // 把反推结果回填到 bars，表自身就自洽了，后续 flush 也不必重算
        if (interval) markInterval.run(interval, symbol, series);
        rollup.run(symbol, series, interval, row.first_t, row.last_t, row.bars, now());
      }
      touched.clear();
      pending = 0;
    },
    stats() {
      return db.prepare(`SELECT COUNT(*) AS bars, COUNT(DISTINCT symbol) AS symbols FROM bars`).get();
    },
    rows({ symbol, limit = 20, since = null }) {
      const where = [];
      const args = [];
      if (symbol) {
        where.push('symbol = ?');
        args.push(symbol);
      }
      if (since != null) {
        where.push('t >= ?');
        args.push(since);
      }
      const sql =
        `SELECT symbol, series, t, o, h, l, c, v FROM bars` +
        (where.length ? ` WHERE ${where.join(' AND ')}` : '') +
        ` ORDER BY t ASC` +
        (limit ? ` LIMIT ${Number(limit)}` : '');
      return db.prepare(sql).all(...args);
    },
    listSeries() {
      return db.prepare(`SELECT * FROM series_meta ORDER BY symbol, series`).all();
    },
    close() {
      this.flush();
      db.close();
    },
  };
}

export function csvEscape(v) {
  if (v == null) return '';
  const s = String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

// 导出用的读取。默认按 (symbol, interval, t) 收敛：
//
// TradingView 的 series id（sds_1 / sds_2 …）是**每条 WebSocket 连接自己发的临时订阅号**，
// 重连后会换号 —— 实测 2026-10-16：页面重连后，同一个 5m 图从 sds_1 变成 sds_2，
// 于是同一根 K 线在库里留下两行（内容一致）。
// 对研究用途来说这是无意义的重复，导出时收敛掉；原始行仍然留在库里可查。
export function readBars(db, { symbol = null, interval = null, dedupe = true, limit = 0 } = {}) {
  const where = [];
  const args = [];
  if (symbol) {
    where.push('symbol = ?');
    args.push(symbol);
  }
  if (interval) {
    where.push('interval = ?');
    args.push(interval);
  }
  const rows = db
    .prepare(
      `SELECT symbol, series, interval, t, o, h, l, c, v, seen_at FROM bars` +
        (where.length ? ` WHERE ${where.join(' AND ')}` : '') +
        ` ORDER BY symbol ASC, t ASC`,
    )
    .all(...args);

  let out = rows;
  let collapsed = 0;
  if (dedupe) {
    const map = new Map();
    for (const r of rows) {
      const key = `${r.symbol}\u0000${r.interval ?? ''}\u0000${r.t}`;
      const prev = map.get(key);
      if (!prev) map.set(key, r);
      else {
        collapsed++;
        if ((r.seen_at || 0) >= (prev.seen_at || 0)) map.set(key, r);
      }
    }
    out = [...map.values()].sort((a, b) =>
      a.symbol === b.symbol ? a.t - b.t : a.symbol < b.symbol ? -1 : 1,
    );
  }
  return { rows: limit ? out.slice(0, limit) : out, total: rows.length, collapsed };
}

// 同一品种下出现过哪些 series —— 用来暴露"重连换号"这件事
export function listSeriesIds(db, symbol = null) {
  return db
    .prepare(
      `SELECT symbol, series, COUNT(*) AS bars, COUNT(DISTINCT t) AS distinct_t,
              MIN(t) AS first_t, MAX(t) AS last_t
       FROM bars` +
        (symbol ? ' WHERE symbol = ?' : '') +
        ` GROUP BY symbol, series ORDER BY symbol, series`,
    )
    .all(...(symbol ? [symbol] : []));
}
