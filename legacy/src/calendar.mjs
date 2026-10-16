// 覆盖视图的数据层：一次计算同时喂"月历"和"时间轴条带"两个视图
//
// 两个视图要的东西其实同源，只是聚合粒度不同：
//   月历     —— 每天一行汇总：抓了多少根、应有几根、缺哪几段、整日有没有数据
//   时间轴   —— 同一天内部，每个时段的 "有没有"（位图），
//               以及所有天叠加起来看"每个时段被覆盖了多少天"（直方图）
//
// 所以这里把一天切成固定数量的桶（5m 图 => 288 桶），每桶一位。
// 一位图比"每个时段的计数"信息量小，但响应体小得多，而且 UI 上就是"有/没有"两态，
// 恰好是渲染热力图需要的东西。
//
// ⚠ 关键点：桶的划分必须按 UTC 天边界切，且每天恒等宽 —— 否则同一天的条带宽度
// 会随跨月、夏令时之类的东西漂移，视觉上就"对不齐"了。

import {
  DAY,
  dayOf,
  slotOf,
  weekdayOfDay,
  dayLabel,
  slotLabel,
  fmtTs,
  intervalSeconds,
  computeCoverage,
} from './coverage.mjs';
import { INTERVAL_NAMES } from './frames.mjs';

// 桶长度阶梯：选最小的 >= 数据周期的那一档，避免出现 1440 格这种没法看的东西
// （1 分钟图仍会落到 60s=1440 格，但那是数据本身决定的，UI 用横向滚动处理）
const BUCKET_LADDER = [60, 120, 180, 300, 600, 900, 1800, 3600, 7200, 14400, 43200, 86400];

export function bucketSeconds(stepSec) {
  if (!stepSec || stepSec <= 0) return 300;
  for (const b of BUCKET_LADDER) if (b >= stepSec) return b;
  return 86400;
}

export function bucketLabel(sec) {
  const name = INTERVAL_NAMES[sec];
  if (name) return name;
  if (sec % 3600 === 0) return `${sec / 3600}h`;
  if (sec % 60 === 0) return `${sec / 60}m`;
  return `${sec}s`;
}

// 主入口。返回的对象直接可以喂给前端，不需要前端再做什么换算。
export function buildCalendar(db, { symbol = null, minConfidence = 0.6, windowDays = 0 } = {}) {
  const sql = 'SELECT symbol, t, interval FROM bars' + (symbol ? ' WHERE symbol = ?' : '') + ' ORDER BY symbol, t';
  const rows = symbol ? db.prepare(sql).all(symbol) : db.prepare(sql).all();
  if (!rows.length) {
    return {
      empty: true,
      symbol,
      interval: null,
      stepSec: null,
      bucketSec: null,
      slotsPerDay: 0,
      days: [],
      histogram: [],
      summary: null,
      months: [],
    };
  }

  // 周期标签：取出现次数最多的那个（一库里一个品种，正常只有一个）
  const labelCounts = new Map();
  for (const r of rows) {
    if (r.interval) labelCounts.set(r.interval, (labelCounts.get(r.interval) || 0) + 1);
  }
  let interval = null;
  let bestN = 0;
  for (const [k, n] of labelCounts) if (n > bestN) [interval, bestN] = [k, n];
  let stepSec = intervalSeconds(interval);
  if (!stepSec) {
    // 兜底：相邻时间戳众数差
    const counts = new Map();
    for (let i = 1; i < rows.length; i++) {
      const d = rows[i].t - rows[i - 1].t;
      if (d > 0) counts.set(d, (counts.get(d) || 0) + 1);
    }
    let best = null;
    for (const [d, n] of counts) if (!best || n > best[1]) best = [d, n];
    stepSec = best ? best[0] : 300;
    interval = INTERVAL_NAMES[stepSec] || `${stepSec}s`;
  }

  const bucketSec = bucketSeconds(stepSec);
  const slotsPerDay = Math.max(1, Math.round(DAY / bucketSec));

  // ---- 逐日状态位图 ----
  // 每格一个字符，四态：
  //   '0' 无数据、且按模式也不该有（休市/结算间歇）
  //   '1' 有数据，且按模式应该有
  //   '2' 应该有但缺 —— 这是真正要盯的东西
  //   '3' 有数据，但按模式不该有（模式样本还不够时会出现）
  // 合成一个字符而不是两个布尔数组，是为了让响应体小一半：
  // 288 格 × 200 天 = 57KB，直接内联进 JSON 完全没问题。
  const dayBits = new Map(); // day -> Uint8Array(slotsPerDay) 仅"有没有"
  const daySlotsSec = new Map(); // day -> Set(精确时段秒)
  const dayCount = new Map(); // day -> 该日不同时间戳数
  const dayFirstSlot = new Map();
  const dayLastSlot = new Map();
  const seenT = new Set();

  for (const r of rows) {
    const d = dayOf(r.t);
    if (!dayBits.has(d)) {
      dayBits.set(d, new Uint8Array(slotsPerDay));
      daySlotsSec.set(d, new Set());
    }
    const raw = slotOf(r.t);
    const idx = Math.min(slotsPerDay - 1, Math.floor(raw / bucketSec));
    if (!dayBits.get(d)[idx]) dayBits.get(d)[idx] = 1;
    daySlotsSec.get(d).add(Math.floor(raw / bucketSec) * bucketSec);
    if (!seenT.has(r.t)) {
      seenT.add(r.t);
      dayCount.set(d, (dayCount.get(d) || 0) + 1);
    }
    if (dayFirstSlot.get(d) == null || raw < dayFirstSlot.get(d)) dayFirstSlot.set(d, raw);
    if (dayLastSlot.get(d) == null || raw > dayLastSlot.get(d)) dayLastSlot.set(d, raw);
  }

  // ---- 借 coverage 的"星期几时段模式"算出每天应有/缺失 ----
  let report = null;
  try {
    const reports = computeCoverage(db, { symbol, minConfidence });
    report = reports[0] || null;
  } catch {
    report = null;
  }
  const covByDay = new Map();
  if (report) for (const d of report.days) covByDay.set(d.day, d);

  const allDays = [...dayBits.keys()].sort((a, b) => a - b);
  let from = allDays[0];
  let to = allDays[allDays.length - 1];
  if (windowDays > 0 && to - from + 1 > windowDays) from = to - windowDays + 1;

  const days = [];
  for (let d = from; d <= to; d++) {
    const bits = dayBits.get(d);
    const cov = covByDay.get(d);
    const presentSec = daySlotsSec.get(d) || new Set();

    // 该日"应该有"的桶 = 当天实有（都是模式认可的）+ coverage 说缺的那些
    const expectedBuckets = new Set();
    if (cov && cov.expected > 0) {
      for (const s of presentSec) expectedBuckets.add(s);
      for (const m of cov.missing) expectedBuckets.add(Math.floor(m / bucketSec) * bucketSec);
    }

    let states = '';
    let filled = 0;
    let missingBuckets = 0;
    // 注意：这里不能写成「没有数据行就直接返回全 0」。
    // 一个完全没有数据的日子恰恰是最该被标成"应有但缺"的那一天，
    // 短路掉就等于把整日空洞从热力图上抹掉了（踩过）。
    const chars = new Array(slotsPerDay);
    for (let i = 0; i < slotsPerDay; i++) {
      const sec = i * bucketSec;
      const has = bits ? bits[i] === 1 : false;
      const want = expectedBuckets.has(sec);
      if (has) filled++;
      if (!has && want) missingBuckets++;
      chars[i] = has ? (want ? '1' : '3') : want ? '2' : '0';
    }
    states = chars.join('');

    days.push({
      day: d,
      iso: dayLabel(d),
      weekday: weekdayOfDay(d),
      n: dayCount.get(d) || 0,
      filledSlots: filled,
      missingSlots: missingBuckets,
      firstSlot: dayFirstSlot.has(d) ? dayFirstSlot.get(d) : null,
      lastSlot: dayLastSlot.has(d) ? dayLastSlot.get(d) : null,
      expected: cov?.expected ?? 0,
      missing: cov ? cov.missing.length : 0,
      status: cov ? cov.status : dayCount.get(d) ? 'unreferenced-data' : 'unknown',
      sampleDays: cov?.sampleDays ?? 0,
      states,
    });
  }

  // ---- 时段直方图：每个桶在多少天里有数据 / 在多少天里应该有 ----
  const hist = new Uint16Array(slotsPerDay);
  const histWant = new Uint16Array(slotsPerDay);
  for (const day of days) {
    for (let i = 0; i < slotsPerDay; i++) {
      const ch = day.states[i];
      if (ch === '1' || ch === '3') hist[i]++;
      if (ch === '1' || ch === '2') histWant[i]++;
    }
  }
  const histogram = Array.from(hist, (n, i) => ({
    slot: i,
    at: i * bucketSec,
    label: slotLabel(i * bucketSec),
    days: n,
    expect: histWant[i],
  }));

  // ---- 月历聚合：每个月有哪些天、以及该月的汇总 ----
  const monthMap = new Map(); // 'YYYY-MM' -> { month, days: [], bars, missing, fullDays, emptyDays }
  for (const d of days) {
    const key = d.iso.slice(0, 7);
    let m = monthMap.get(key);
    if (!m) {
      m = { month: key, days: [], bars: 0, missing: 0, fullDays: 0, emptyDays: 0, expected: 0 };
      monthMap.set(key, m);
    }
    m.days.push(d);
    m.bars += d.n;
    m.missing += d.missing;
    m.expected += d.expected;
    if (d.n === 0) m.emptyDays++;
    else if (d.expected > 0 && d.missing === 0) m.fullDays++;
  }
  const months = [...monthMap.values()].sort((a, b) => (a.month < b.month ? -1 : 1));

  return {
    empty: false,
    symbol: report?.symbol ?? symbol,
    interval,
    stepSec,
    bucketSec,
    bucket: bucketLabel(bucketSec),
    slotsPerDay,
    range: { from, to, fromIso: dayLabel(from), toIso: dayLabel(to), days: to - from + 1 },
    days,
    histogram,
    months,
    summary: report
      ? {
          bars: report.bars,
          first: report.first,
          last: report.last,
          spanDays: report.spanDays,
          coveredDays: report.coveredDays,
          expectedTotal: report.expectedTotal,
          matched: report.matched,
          missingTotal: report.missingTotal,
          coverageRatio: report.coverageRatio,
          patternWeak: report.patternWeak,
          minSample: report.minSample,
          weekdaySamples: report.weekdaySamples,
          unknownDays: report.unknownDays,
          // 断档补上人看的标签。coverage 里存的是时间戳（算的时候只关心数），
          // 到了要展示的这一层就该是字符串，别让前端各自去格式化。
          breaks: report.breaks.slice(0, 20).map((b) => ({
            fromTs: b.fromTs,
            toTs: b.toTs,
            fromIso: fmtTs(b.fromTs),
            toIso: fmtTs(b.toTs),
            seconds: b.seconds,
            missing: b.missing,
          })),
          breakCount: report.breaks.length,
          topGaps: report.gaps.slice(0, 60).map((g) => ({
            day: g.day,
            iso: dayLabel(g.day),
            weekday: g.weekday,
            present: g.present,
            expected: g.expected,
            missingCount: g.missing.length,
            missing: g.missing.slice(0, 40).map((s) => slotLabel(s)),
          })),
          gapDays: report.gaps.length,
          nextTarget: report.gaps.length
            ? { day: report.gaps[0].day, iso: dayLabel(report.gaps[0].day), missingCount: report.gaps[0].missing.length }
            : null,
        }
      : null,
  };
}

// 给单日做明细：把一天的格子合并成"有数据的连续区间"，
// UI 展开某天时展示"00:00–08:55 / 13:00–20:55"这种更好读的形式。
// 状态字符见 buildCalendar 的说明：'1' 有数据、'3' 有数据但按模式不该有，两者都算「有」。
export function daySegments(day, bucketSec) {
  const states = day?.states;
  if (!states) return [];
  const segs = [];
  let start = -1;
  for (let i = 0; i < states.length; i++) {
    const on = states[i] === '1' || states[i] === '3';
    if (on && start < 0) start = i;
    if (!on && start >= 0) {
      segs.push({ from: start * bucketSec, to: i * bucketSec });
      start = -1;
    }
  }
  if (start >= 0) segs.push({ from: start * bucketSec, to: states.length * bucketSec });
  return segs.map((s) => ({ ...s, label: `${slotLabel(s.from)}–${slotLabel(s.to)}` }));
}
