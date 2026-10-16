// 服务端只读视图：把库里/盘上的东西整理成前端直接能用的形状
//
// 单独放一层的理由：这些函数都不依赖 HTTP，可以单独跑测试；
// 而且"前端要什么"和"HTTP 怎么传"是两件事，混在一起改起来会互相牵连。

import fs from 'node:fs';
import path from 'node:path';
import {
  listDbFiles,
  symbolForSlug,
  fileForSymbol,
  symbolSlug,
  loadIndex,
  saveIndex,
  UNKNOWN_SYMBOL,
} from '../src/registry.mjs';
import { openStore, safeSummarize, getMeta, readBars, csvEscape } from '../src/store.mjs';
import { computeCoverage, dayLabel, slotLabel, fmtTs } from '../src/coverage.mjs';
import { buildCalendar, bucketSeconds, bucketLabel, daySegments } from '../src/calendar.mjs';
import { listRawFiles, reconcileRawIndex } from '../src/rawindex.mjs';
import { readLedger } from '../src/ingest.mjs';

// 磁盘上有库文件、但索引里没登记的（用户手工拷进来的）：
// 打开读一下自身的 meta.symbol，有就补登记。没有就按文件名当品种名。
// 这样"我把同事的库拷过来"这种操作是开箱可用的。
export function reconcileIndex(root) {
  const files = listDbFiles(root);
  const idx = loadIndex(root);
  let changed = false;
  for (const f of files) {
    if (f.indexed || f.missing) continue;
    let symbol = null;
    let db = null;
    try {
      db = openStore(f.file);
      symbol = getMeta(db, 'symbol', null);
    } catch {
      symbol = null;
    } finally {
      try {
        db?.close();
      } catch {}
    }
    const sym = symbol || f.slug;
    // 别把已有映射踩掉
    if (!idx.symbols[sym]) {
      idx.symbols[sym] = { file: f.filename, slug: f.slug, created: Date.now(), adopted: true };
      changed = true;
    }
  }
  if (changed) saveIndex(root, idx);
  return changed;
}

export function resolveSymbolBySlug(root, slug) {
  const direct = symbolForSlug(root, slug);
  if (direct) return direct;
  // 索引里查不到：可能是刚拷进来的文件，先对账一次再试
  reconcileIndex(root);
  return symbolForSlug(root, slug);
}

export function dbPathForSlug(root, slug) {
  const sym = resolveSymbolBySlug(root, slug);
  if (sym) return fileForSymbol(root, sym);
  const files = listDbFiles(root);
  const hit = files.find((f) => f.slug === slug);
  if (hit) return hit.file;
  const e = new Error(`没有找到 slug 为 ${slug} 的品种库`);
  e.status = 404;
  throw e;
}

// 品种列表（数据管理页的主列表）
export function listSymbols(root) {
  reconcileIndex(root);
  const files = listDbFiles(root);
  const out = [];
  for (const f of files) {
    if (f.missing) {
      out.push({
        slug: f.slug,
        symbol: f.symbol,
        filename: f.filename,
        missing: true,
        bars: 0,
        rows: 0,
        series: [],
      });
      continue;
    }
    const sym = f.symbol || null;
    const summary = safeSummarize(f.file, { symbol: sym });
    const stat = fs.existsSync(f.file) ? fs.statSync(f.file) : null;
    out.push({
      slug: f.slug,
      symbol: summary.symbol || sym || f.slug,
      filename: f.filename,
      file: f.file,
      sizeBytes: stat?.size ?? 0,
      missing: false,
      ...summary,
    });
  }
  return out.sort((a, b) => (b.bars || 0) - (a.bars || 0));
}

// 完整覆盖度报告（缺口 + 断档），把内部用的时间戳/槽位同时翻译成人看的标签
export function coverageFor(root, slug, { minConfidence = 0.6, top = 40 } = {}) {
  const file = dbPathForSlug(root, slug);
  const db = openStore(file);
  try {
    const reports = computeCoverage(db, { minConfidence });
    if (!reports.length) return null;
    const r = reports[0];
    return {
      symbol: r.symbol,
      interval: r.interval,
      stepSec: r.stepSec,
      bars: r.bars,
      first: r.first,
      last: r.last,
      firstIso: fmtTs(r.first),
      lastIso: fmtTs(r.last),
      spanDays: r.spanDays,
      coveredDays: r.coveredDays,
      expectedTotal: r.expectedTotal,
      matched: r.matched,
      missingTotal: r.missingTotal,
      coverageRatio: r.coverageRatio,
      patternWeak: r.patternWeak,
      minSample: r.minSample,
      weekdaySamples: r.weekdaySamples,
      unknownDays: r.unknownDays.map((d) => dayLabel(d)),
      gapDays: r.gaps.length,
      gaps: r.gaps.slice(0, top).map((g) => ({
        day: g.day,
        iso: dayLabel(g.day),
        weekday: g.weekday,
        present: g.present,
        expected: g.expected,
        missingCount: g.missing.length,
        missingLabels: g.missing.slice(0, 60).map((s) => slotLabel(s)),
        status: g.status,
      })),
      breaks: r.breaks.slice(0, 40).map((b) => ({
        fromTs: b.fromTs,
        toTs: b.toTs,
        fromIso: fmtTs(b.fromTs),
        toIso: fmtTs(b.toTs),
        seconds: b.seconds,
        missing: b.missing,
      })),
      breakCount: r.breaks.length,
      nextTarget: r.gaps.length
        ? { iso: dayLabel(r.gaps[0].day), missingCount: r.gaps[0].missing.length }
        : null,
    };
  } finally {
    db.close();
  }
}

// 日历 + 时间轴（一个接口同时喂两个视图）
export function calendarFor(root, slug, { windowDays = 0, minConfidence = 0.6, day = null } = {}) {
  const file = dbPathForSlug(root, slug);
  const db = openStore(file);
  try {
    const cal = buildCalendar(db, { minConfidence, windowDays });
    if (day) {
      // 点了哪天就一定要给个答复。
      //
      // 月历会把整月都画成格子（数据没覆盖到的那几天也画，标成"不在范围"），
      // 所以 `day` 完全可能落在 cal.days 之外 —— 之前的写法是这时候 focus 留 null，
      // 于是页面出现了「标题说选中了 2026-10-01，正文却还是"点左边日历里任意一天"」
      // 这种自相矛盾的状态。用户点了有反应却看不到东西，比明确说"这天没数据"差得多。
      const target = cal.days.find((d) => d.iso === day);
      if (target) {
        cal.focus = {
          iso: target.iso,
          segments: daySegments(target, cal.bucketSec).map((s) => s.label),
          segmentsRaw: daySegments(target, cal.bucketSec),
          n: target.n,
          expected: target.expected,
          missing: target.missing,
          status: target.status,
          states: target.states,
          filledSlots: target.filledSlots,
          missingSlots: target.missingSlots,
        };
      } else if (/^\d{4}-\d{2}-\d{2}$/.test(day)) {
        // 落在数据范围之外（或该月整月都没被这些格子覆盖）
        const inRange = cal.range ? day >= cal.range.fromIso && day <= cal.range.toIso : false;
        cal.focus = {
          iso: day,
          segments: [],
          segmentsRaw: [],
          n: 0,
          expected: 0,
          missing: 0,
          status: inRange ? 'absent' : 'out-of-range',
          states: '',
          filledSlots: 0,
          missingSlots: 0,
        };
      }
    }
    return cal;
  } finally {
    db.close();
  }
}

export function barsFor(root, slug, { limit = 2000, from = null, to = null, interval = null } = {}) {
  const file = dbPathForSlug(root, slug);
  const db = openStore(file);
  try {
    const { rows, total, collapsed } = readBars(db, { interval, dedupe: true });
    let out = rows;
    if (from != null) out = out.filter((r) => r.t >= from);
    if (to != null) out = out.filter((r) => r.t <= to);
    const trimmed = limit > 0 ? out.slice(-limit) : out;
    return { total, collapsed, symbol: getMeta(db, 'symbol', null), rows: trimmed };
  } finally {
    db.close();
  }
}

export function exportCsv(root, slug) {
  const file = dbPathForSlug(root, slug);
  const db = openStore(file);
  try {
    const { rows, total, collapsed } = readBars(db, { dedupe: true });
    const lines = ['symbol,interval,t,iso,o,h,l,c,v'];
    for (const r of rows) {
      lines.push(
        [
          csvEscape(r.symbol),
          csvEscape(r.interval),
          r.t,
          new Date(r.t * 1000).toISOString(),
          r.o,
          r.h,
          r.l,
          r.c,
          r.v,
        ].join(','),
      );
    }
    const symbol = getMeta(db, 'symbol', slug) || slug;
    return {
      symbol,
      filename: `${symbolSlug(symbol)}_${new Date().toISOString().slice(0, 10)}.csv`,
      csv: lines.join('\n') + '\n',
      rows: rows.length,
      total,
      collapsed,
    };
  } finally {
    db.close();
  }
}

// 原始抓包文件列表 + 台账状态（数据管理页的下半部分）
export function rawFilesView(root, { symbol = null } = {}) {
  // 先把还没登记过的历史文件对一遍账，否则老文件在界面上会显示"品种未知"
  try {
    reconcileRawIndex(root);
  } catch {}
  const ledger = readLedger(root);
  return listRawFiles(root, { symbol })
    .slice()
    .reverse()
    .map((f) => {
      const led = ledger[f.name];
      return {
        name: f.name,
        symbol: f.symbol,
        url: f.url,
        sizeBytes: f.size,
        mtime: f.mtimeMs,
        at: new Date(f.mtimeMs).toISOString(),
        ingested: Boolean(led),
        ingestedAt: led?.at ?? null,
        ingestedBars: led?.bars ?? null,
        pending: !led || led.size !== f.size || led.mtimeMs !== f.mtimeMs,
      };
    });
}

export function registryInfo(root) {
  const idx = loadIndex(root);
  const files = listDbFiles(root);
  return {
    symbols: Object.entries(idx.symbols).map(([symbol, rec]) => ({
      symbol,
      file: rec.file,
      slug: rec.slug,
      created: rec.created,
      adopted: Boolean(rec.adopted),
      onDisk: files.some((f) => f.filename === rec.file),
    })),
    unknown: UNKNOWN_SYMBOL,
    bucketHelpers: {
      bucketSeconds,
      bucketLabel,
    },
  };
}
