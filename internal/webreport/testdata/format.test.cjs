// 前端那一段纯函数（格式化 / 筛选 / 排序）的断言，交给 node 跑。
//
// 为什么要单独测：app.js 里的 num / bytes / gap 是 Go 侧 comma / humanBytes / gapText
// 的第二份实现 —— 统计量不会漂移（那部分只在 Go 里算），但**显示出来的数字**会。
// 一旦有人只改了 Go 那一侧，用户就会看到「终端写 1时5分、网页写 65分」。
// 所以这里的期望值刻意照抄 report_test.go 的那张表：两边都改才算改对。
//
// 由 Go 侧的 TestFrontendFormattingMatchesGo 调起来（没有 node 就跳过），
// 也可以手工跑：node internal/webreport/testdata/format.test.cjs

const fs = require('fs');
const path = require('path');

const APP = path.join(__dirname, '..', 'static', 'app.js');
const MARK = '/* ---------- 应用 ---------- */';

const src = fs.readFileSync(APP, 'utf8');
const cut = src.indexOf(MARK);
if (cut < 0) {
  console.error('找不到纯函数区的分隔标记 ' + MARK + '，app.js 的结构变了，这个测试要跟着改');
  process.exit(1);
}

// 只取 createApp 之前的那一半：不碰 Vue、不碰 DOM，在 node 里能直接跑
const mod = { exports: {} };
const code = src.slice(0, cut) +
  '\nmodule.exports = { num, bytes, gap, clock, stamp, dayIssue, isDateQuery, sortFiles, sumOf, alertsOf,' +
  ' monthOf, isMonth, addMonth, todayMonth, monthCells, weekday, monthsOf };\n';
new Function('module', 'exports', code)(mod, mod.exports);
const f = mod.exports;

let checks = 0;
function eq(actual, want, label) {
  checks++;
  const a = JSON.stringify(actual), w = JSON.stringify(want);
  if (a !== w) {
    console.error('✗ ' + label + '\n  得到 ' + a + '\n  期望 ' + w);
    process.exit(1);
  }
}
function ok(cond, label) {
  checks++;
  if (!cond) {
    console.error('✗ ' + label);
    process.exit(1);
  }
}

// ---- 这三个的期望值和 report_test.go / analyze.go 逐条对齐 ----
eq(f.num(0), '0', 'num(0)');
eq(f.num(999), '999', 'num(999)');
eq(f.num(1000), '1,000', 'num(1000)');
eq(f.num(10822), '10,822', 'num(10822)');
eq(f.num(1234567), '1,234,567', 'num(1234567)');
eq(f.num(undefined), '0', 'num(undefined) 不该变成 NaN');

eq(f.bytes(512), '512 B', 'bytes(512)');
eq(f.bytes(15037), '14.7 KB', 'bytes(15037)');
eq(f.bytes(573 * 1024), '573.0 KB', 'bytes(573KB)');
eq(f.bytes(5 * 1024 * 1024), '5.0 MB', 'bytes(5MB)');

eq(f.gap(45), '45秒', 'gap(45)');
eq(f.gap(300), '5分', 'gap(300)');
eq(f.gap(3900), '1时5分', 'gap(3900)');
eq(f.gap(7200), '2时', 'gap(7200)');

// ---- 时间：不钉死具体时区（换台机器跑就会红），只钉格式和内部一致性 ----
const t = Math.floor(Date.UTC(2026, 0, 2, 3, 4) / 1000);
ok(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(f.stamp(t)), 'stamp 的格式应当是 yyyy-MM-dd HH:MM：' + f.stamp(t));
ok(/^\d{2}:\d{2}$/.test(f.clock(t)), 'clock 的格式应当是 HH:MM：' + f.clock(t));
eq(f.clock(t), f.stamp(t).slice(11), 'clock 和 stamp 必须来自同一个时刻');

// ---- 筛选口径 ----
ok(f.dayIssue({ err: '读不了' }), '整份读不了算异常');
ok(f.dayIssue({ skipped: 1 }), '坏行算异常');
ok(f.dayIssue({ dupes: 2 }), '重复行算异常');
ok(!f.dayIssue({ bars: 276, missing: 12, skipped: 0, dupes: 0 }), '缺口不算异常（休市是常态）');

ok(f.isDateQuery('2026-10'), '日期查询');
ok(f.isDateQuery('2026'), '纯数字也当日期');
ok(!f.isDateQuery('MES'), '标的名不是日期查询');
ok(!f.isDateQuery(''), '空查询词');

// ---- 排序：不原地改数组，同值时按日期升序兜底 ----
const files = [
  { date: '2026-10-16', bars: 276, missing: 12, maxGap: 3900, bytes: 15037 },
  { date: '2026-10-15', bars: 288, missing: 0, maxGap: 300, bytes: 16000 },
  { date: '2026-10-14', bars: 276, missing: 5, maxGap: 600, bytes: 13000 },
];
const byBarsDesc = f.sortFiles(files, 'bars', -1).map(function (d) { return d.date; });
eq(byBarsDesc, ['2026-10-15', '2026-10-14', '2026-10-16'], '根数降序（同值按日期升序兜底）');
const byDateAsc = f.sortFiles(files, 'date', 1).map(function (d) { return d.date; });
eq(byDateAsc, ['2026-10-14', '2026-10-15', '2026-10-16'], '日期升序');
const byGapDesc = f.sortFiles(files, 'maxGap', -1).map(function (d) { return d.date; });
eq(byGapDesc, ['2026-10-16', '2026-10-14', '2026-10-15'], '断档降序');
eq(files[0].date, '2026-10-16', 'sortFiles 不能就地改原数组（Vue 里那份是响应式数据）');

eq(f.sumOf(files), { bars: 840, missing: 17, bytes: 44037 }, '小计');

// ---- 日历：纯日期算术，期望值不依赖跑测试的机器 ----
eq(f.monthOf('2026-10-16'), '2026-10', 'monthOf 取年月');
eq(f.monthOf('2026-10'), '2026-10', '已经是月份时原样返回');
eq(f.monthOf(''), '', '空串不该炸');
ok(f.isMonth('2026-10'), '合法月份');
ok(!f.isMonth('2026'), '只有年份不是月份');
ok(!f.isMonth('2026-13'), '13 月不是月份');

// 前后翻月必须跨年正确 —— 手写进位最常在这里翻车，所以一律交给 Date
eq(f.addMonth('2026-01', -1), '2025-12', '往前翻跨年');
eq(f.addMonth('2026-12', 1), '2027-01', '往后翻跨年');
eq(f.addMonth('2026-10', -12), '2025-10', '往前翻一整年');

// 2026-10-01 是周四 → 周一打头的日历前面要空 3 格
const oct = f.monthCells('2026-10');
eq(oct.slice(0, 3).map(function (c) { return c.date; }), ['', '', ''], '10 月 1 日前的补位格');
eq(oct[3], { date: '2026-10-01', day: 1 }, '补位之后第一个是 10 月 1 日');
eq(oct.length, 35, '31 天 + 前补 3 = 34，尾部补满整周后是 35');
const octDays = oct.filter(function (c) { return c.date; });
eq(octDays.length, 31, '10 月应当有 31 个格子');
eq(octDays[30].day, 31, '最后一个有日期的格子是 31 日');
eq(octDays[30].date, '2026-10-31', '最后一格的日期');
ok(oct.length % 7 === 0, '格子数永远是 7 的整数倍，否则日历会缺一角');
// 闰年 2 月：2024 年是闰年（能被 4 整除且不是整百年）
eq(f.monthCells('2024-02').filter(function (c) { return c.date; }).length, 29, '2024 年 2 月有 29 天');
eq(f.monthCells('2026-02').filter(function (c) { return c.date; }).length, 28, '2026 年 2 月有 28 天');
eq(f.monthCells('乱写的'), [], '不是月份就返回空，不抛异常');

ok(/^\d{4}-\d{2}$/.test(f.todayMonth()), 'todayMonth 的格式应当是 yyyy-MM：' + f.todayMonth());
eq(f.todayMonth(), f.monthOf(f.stamp(Date.now() / 1000)), 'todayMonth 和 stamp 必须来自同一个时刻');

eq(f.weekday('2026-10-16'), '周五', 'weekday 认得出周五');
eq(f.weekday('2026-10-18'), '周日', 'weekday 认得出周日');
eq(f.weekday(''), '', '空日期不给星期');

eq(f.monthsOf(['2026-10-16', '2026-09-01', '2026-10-02']), ['2026-10', '2026-09'], '月份去重且倒序');
eq(f.monthsOf(['2026-10-16', '乱写的']), ['2026-10'], '认不出来的日期直接跳过');
eq(f.monthsOf([]), [], '没有日期就没有月份');

// ---- 告警：notes 原样透传，坏行/重复行再补一句 ----
const alerts = f.alertsOf({
  notes: ['数据目录还不存在（还没抓过数据？）'],
  totals: { skipped: 2, dupes: 1 },
});
eq(alerts.length, 2, '告警条数');
eq(alerts[0].text, '数据目录还不存在（还没抓过数据？）', 'notes 原样透传');
ok(alerts[1].text.indexOf('2 行解析不了') >= 0 && alerts[1].text.indexOf('1 行时间戳重复') >= 0,
  '坏行/重复行的汇总文案：' + alerts[1].text);
eq(f.alertsOf({ notes: [], totals: {} }).length, 0, '一切正常时不该有告警');

console.log('OK  ' + checks + ' 项断言通过（' + path.basename(APP) + ' 与 Go 侧口径一致）');
