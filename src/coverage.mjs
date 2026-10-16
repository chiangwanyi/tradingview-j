// 覆盖度报告：找出时间轴上还没抓到的空洞
//
// 为什么不能简单地"从最早按周期步进到最晚"：
// 期货有每日结算间歇，还有周末休市。机械步进会把所有正常休市都报成缺失，
// 噪声大到看不出真正漏抓的是哪天。
//
// 这里改成从数据自身学习「时段模式」：
//   1. 对每个星期几，统计历史上出现过哪些 HH:MM 时段（UTC）；
//   2. 某时段在多数同类日子里都出现 -> 视为"应当存在"；
//   3. 再把区间内每个日历日与该模式比对，列出缺哪些时段。
// 因为模式是从数据里学的，周末和休市天然不产生噪声；真正漏抓的日子会被点名。
//
// 一个诚实的边界：某个星期几如果一天样本都没有，无法判断它该长什么样，
// 报告会标成「无参照」而不是假装"完整"。样本越多，判断越准。

import { INTERVAL_NAMES } from './frames.mjs';

const WD_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

const SEC_BY_LABEL = Object.fromEntries(Object.entries(INTERVAL_NAMES).map(([k, v]) => [v, Number(k)]));

export function intervalSeconds(label) {
  if (!label) return null;
  if (SEC_BY_LABEL[label]) return SEC_BY_LABEL[label];
  const m = /^(\d+)s$/.exec(String(label));
  return m ? Number(m[1]) : null;
}

function modalStep(ts) {
  const counts = new Map();
  for (let i = 1; i < ts.length; i++) {
    const d = ts[i] - ts[i - 1];
    if (d > 0) counts.set(d, (counts.get(d) || 0) + 1);
  }
  let best = null;
  for (const [d, n] of counts) if (!best || n > best[1]) best = [d, n];
  return best ? best[0] : null;
}

export const DAY = 86400;
export const dayOf = (t) => Math.floor(t / DAY);
export const slotOf = (t) => ((t % DAY) + DAY) % DAY;
// 1970-01-01 是周四(4)
export const weekdayOfDay = (d) => ((d + 4) % 7 + 7) % 7;

export function dayLabel(day) {
  return new Date(day * DAY * 1000).toISOString().slice(0, 10);
}

export function slotLabel(slot) {
  const h = Math.floor(slot / 3600);
  const m = Math.floor((slot % 3600) / 60);
  const s = slot % 60;
  return s ? `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
           : `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
}

export function fmtTs(t) {
  return t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 19) : '-';
}

// 断档：相邻时间戳间隔明显大于一个周期的位置。
// 这一节不做任何"交易时段"的假设，纯看数据本身被切成了几块 ——
// 目的是给上一节的"模式比对"当交叉验证：模式比对说"完整"，但断档列表里
// 若躺着一段几十小时的空白，那就是模式样本太少导致的假绿灯。
function findBreaks(ts, stepSec) {
  if (!stepSec) return [];
  const threshold = stepSec * 1.5;
  const breaks = [];
  for (let i = 1; i < ts.length; i++) {
    const d = ts[i] - ts[i - 1];
    if (d > threshold) {
      breaks.push({
        fromTs: ts[i - 1],
        toTs: ts[i],
        seconds: d,
        missing: Math.max(0, Math.round(d / stepSec) - 1),
        fromDay: dayOf(ts[i - 1]),
        toDay: dayOf(ts[i]),
      });
    }
  }
  return breaks.sort((a, b) => b.seconds - a.seconds);
}

function humanDuration(sec) {
  if (sec < 3600) return `${Math.round(sec / 60)} 分钟`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} 小时`;
  return `${(sec / 86400).toFixed(1)} 天`;
}

// 单个 symbol 的覆盖度分析
function analyze(symbol, list, minConfidence) {
  const ts = list.map((r) => r.t).sort((a, b) => a - b);
  const first = ts[0];
  const last = ts[ts.length - 1];

  // 周期标签：优先用入库时已定好的，退回相邻差值众数
  const labelCounts = new Map();
  for (const r of list) {
    if (r.interval) labelCounts.set(r.interval, (labelCounts.get(r.interval) || 0) + 1);
  }
  let interval = null;
  let bestN = 0;
  for (const [k, n] of labelCounts) if (n > bestN) [interval, bestN] = [k, n];
  let stepSec = intervalSeconds(interval);
  if (!stepSec) {
    stepSec = modalStep(ts);
    interval = stepSec ? INTERVAL_NAMES[stepSec] || `${stepSec}s` : null;
  }

  // 每个日历日的时段集合
  const daySlots = new Map();
  for (const t of ts) {
    const d = dayOf(t);
    if (!daySlots.has(d)) daySlots.set(d, new Set());
    daySlots.get(d).add(slotOf(t));
  }

  // 按星期几学习"应当存在"的时段
  const perWeekday = new Map(); // wd -> Map(day -> Set(slot))
  for (const [d, slots] of daySlots) {
    const wd = weekdayOfDay(d);
    if (!perWeekday.has(wd)) perWeekday.set(wd, new Map());
    perWeekday.get(wd).set(d, slots);
  }
  const expected = new Map(); // wd -> { slots, sampleDays }
  for (const [wd, days] of perWeekday) {
    const freq = new Map();
    for (const slots of days.values()) for (const s of slots) freq.set(s, (freq.get(s) || 0) + 1);
    const need = Math.max(1, Math.ceil(days.size * minConfidence));
    const slots = new Set();
    for (const [s, c] of freq) if (c >= need) slots.add(s);
    expected.set(wd, { slots, sampleDays: days.size });
  }

  // 逐日比对
  const firstDay = dayOf(first);
  const lastDay = dayOf(last);
  const days = [];
  let expectedTotal = 0;
  let matched = 0;

  for (let d = firstDay; d <= lastDay; d++) {
    const wd = weekdayOfDay(d);
    const exp = expected.get(wd);
    const present = daySlots.get(d) || new Set();

    if (!exp || exp.slots.size === 0) {
      days.push({
        day: d,
        weekday: wd,
        status: present.size ? 'unreferenced-data' : 'unknown',
        present: present.size,
        expected: 0,
        missing: [],
        sampleDays: exp?.sampleDays ?? 0,
      });
      continue;
    }

    const missing = [];
    for (const s of exp.slots) if (!present.has(s)) missing.push(s);
    missing.sort((a, b) => a - b);
    expectedTotal += exp.slots.size;
    matched += exp.slots.size - missing.length;

    days.push({
      day: d,
      weekday: wd,
      status: missing.length === 0 ? 'full' : missing.length >= exp.slots.size * 0.5 ? 'gap-big' : 'gap',
      present: present.size,
      expected: exp.slots.size,
      missing,
      sampleDays: exp.sampleDays,
    });
  }

  const gaps = days.filter((x) => x.missing.length);
  const unknown = days.filter((x) => x.status === 'unknown');
  const breaks = findBreaks(ts, stepSec);

  const weekdaySamples = Object.fromEntries([...expected].map(([wd, e]) => [wd, e.sampleDays]));
  const sampleCounts = Object.values(weekdaySamples);
  const minSample = sampleCounts.length ? Math.min(...sampleCounts) : 0;
  // 判断"参照是否够用"：每个星期几只见过 1~2 天的话，模式几乎等于照抄那一天，
  // 报出来的高覆盖率没有意义。这里把它挑明，而不是给用户一个虚假的绿灯。
  const patternWeak = expected.size < 5 || minSample < 3;

  return {
    symbol,
    interval,
    stepSec,
    bars: list.length,
    first,
    last,
    spanDays: lastDay - firstDay + 1,
    coveredDays: [...daySlots.keys()].length,
    expectedTotal,
    matched,
    missingTotal: expectedTotal - matched,
    coverageRatio: expectedTotal ? matched / expectedTotal : null,
    days,
    gaps: gaps.sort((a, b) => b.missing.length - a.missing.length),
    unknownDays: unknown.map((x) => x.day),
    weekdaySamples,
    minSample,
    patternWeak,
    breaks,
  };
}

export function computeCoverage(db, { symbol = null, minConfidence = 0.6 } = {}) {
  const sql = 'SELECT symbol, t, interval FROM bars' + (symbol ? ' WHERE symbol = ?' : '') + ' ORDER BY symbol, t';
  const rows = symbol ? db.prepare(sql).all(symbol) : db.prepare(sql).all();
  if (!rows.length) return [];

  const bySymbol = new Map();
  for (const r of rows) {
    if (!bySymbol.has(r.symbol)) bySymbol.set(r.symbol, []);
    bySymbol.get(r.symbol).push(r);
  }
  return [...bySymbol].map(([sym, list]) => analyze(sym, list, minConfidence));
}

// 命令行长报告
export function renderCoverage(report, { maxDays = 15, maxBreaks = 8 } = {}) {
  const out = [];
  const pct = report.coverageRatio == null ? '?' : (report.coverageRatio * 100).toFixed(1) + '%';
  out.push(
    `${report.symbol} · ${report.interval || '周期未知'} · ${report.bars} 根 · ` +
      `相邻间隔 ${report.stepSec || '?'}s`,
  );
  out.push(`   范围   ${fmtTs(report.first)} → ${fmtTs(report.last)}（UTC）`);
  out.push(
    `   覆盖   跨 ${report.spanDays} 天 / 有数据 ${report.coveredDays} 天 · ` +
      `应到 ${report.expectedTotal} 根 · 实到 ${report.matched} 根 · 缺 ${report.missingTotal} 根（${pct}）`,
  );

  const samples = Object.entries(report.weekdaySamples)
    .map(([wd, n]) => `${WD_CN[wd]}×${n}`)
    .join(' ');
  out.push(`   参照   每个星期几的样本天数：${samples || '（无）'}`);

  if (report.patternWeak) {
    out.push(
      `   ⚠ 参照样本不足（每个星期几最少只有 ${report.minSample} 天），上面的覆盖率偏乐观，` +
        `只适合当"有没有明显大洞"的粗筛。继续用回放往不同日期填，样本攒够后这个数字才可信。`,
    );
  }
  if (report.unknownDays.length) {
    const un = report.unknownDays;
    out.push(
      `   ⚠ ${un.length} 天属于「无参照」星期几（从没抓到过同类日子的样本），无法判定完整性：` +
        un.slice(0, 6).map(dayLabel).join(' ') +
        (un.length > 6 ? ` …等 ${un.length} 天` : ''),
    );
  }

  if (!report.gaps.length) {
    out.push('   缺失   按当前参照模式：无');
  } else {
    out.push(`   缺失   共 ${report.gaps.length} 天有缺口，按缺口大小排序（前 ${Math.min(maxDays, report.gaps.length)}）：`);
    for (const d of report.gaps.slice(0, maxDays)) {
      const head = `${dayLabel(d.day)} ${WD_CN[d.weekday]}`;
      const whole = d.present === 0 ? '整日无数据 · ' : '';
      const sample = d.missing.slice(0, 6).map(slotLabel).join(' ');
      const more = d.missing.length > 6 ? ` …（共缺 ${d.missing.length} 个时段）` : '';
      out.push(`     ${head}  缺 ${d.missing.length}/${d.expected}   ${whole}${sample}${more}`);
    }
    if (report.gaps.length > maxDays) out.push(`     …另有 ${report.gaps.length - maxDays} 天有缺口，加 --top N 看更多`);
  }

  // 交叉验证：不依赖任何交易时段假设，纯看时间轴被切成几块
  out.push('');
  out.push(`   断档   时间轴被切成 ${report.breaks.length + 1} 段，最长的 ${Math.min(maxBreaks, report.breaks.length)} 处空白：`);
  if (!report.breaks.length) {
    out.push('     （无 —— 相邻 K 线间隔都在一个周期内）');
  } else {
    for (const b of report.breaks.slice(0, maxBreaks)) {
      out.push(
        `     ${fmtTs(b.fromTs)} → ${fmtTs(b.toTs)}  ${humanDuration(b.seconds).padStart(9)}  约缺 ${b.missing} 根`,
      );
    }
    if (report.breaks.length > maxBreaks) out.push(`     …另有 ${report.breaks.length - maxBreaks} 处`);
    const dayBreaks = report.breaks.filter((b) => b.seconds < 3 * DAY).length;
    out.push(
      `     其中 ${dayBreaks} 处短于 3 天（多半是每日结算间歇/周末休市这一类正常断开），` +
        `${report.breaks.length - dayBreaks} 处长于 3 天（这类才更可能是真的漏抓）。`,
    );
  }

  if (report.gaps.length) {
    const worst = report.gaps[0];
    out.push('');
    out.push(`   建议下一个回放目标：${dayLabel(worst.day)}（该日缺 ${worst.missing.length} 个时段，最值得补）`);
  }
  return out.join('\n');
}

// 常驻模式用的一行摘要
export function coverageLine(report) {
  if (!report) return '(无数据)';
  const pct = report.coverageRatio == null ? '?' : (report.coverageRatio * 100).toFixed(1) + '%';
  return (
    `${report.symbol} ${report.interval || '?'} ${report.bars} 根 · ` +
    `${dayLabel(dayOf(report.first))} → ${dayLabel(dayOf(report.last))} · 缺 ${report.missingTotal} 根 (${pct})`
  );
}
