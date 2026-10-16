// 前端纯函数的离线自测。
//
// 思路来自「把 app.js 里 createApp 之前的部分切出来 eval」——
// 那些格式化 / 布局 / 命中判定的函数不需要浏览器就能断言，
// 比开个无头浏览器快几十倍。等它们都绿了，再去验真正需要布局引擎的界面。
//
// 运行：node test/ui-selftest.mjs

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SRC = path.join(ROOT, 'web', 'app.js');

let passed = 0;
let failed = 0;
function check(name, fn) {
  try {
    fn();
    console.log(`  ok   ${name}`);
    passed++;
  } catch (e) {
    console.log(`  FAIL ${name}\n       ${e.message}`);
    failed++;
    process.exitCode = 1;
  }
}

// ---- 把纯函数那一段切出来跑 ----
const raw = fs.readFileSync(SRC, 'utf8');
const marker = '/* ---------- 应用 ---------- */';
const idx = raw.indexOf(marker);
if (idx < 0) {
  console.error(`在 web/app.js 里找不到分隔标记 "${marker}"，无法切出纯函数段。`);
  process.exit(2);
}
let head = raw.slice(0, idx).replace('const { createApp } = Vue;', '');
head += `
module.exports = {
  fmtInt, fmtBytes, fmtDur, fmtDurShort, fmtClock, fmtTs, fmtPct,
  buildMonthGrid, statusText, dayStatusText, dayRatio,
  computeMatrixLayout, matrixHitTest, slotState, MATRIX_DEFAULTS,
};
`;
const mod = { exports: {} };
new Function('module', 'exports', head)(mod, mod.exports);
const F = mod.exports;

console.log('格式化');
check('fmtInt：千分位，空值给占位符', () => {
  assert.equal(F.fmtInt(2576), '2,576');
  assert.equal(F.fmtInt(0), '0');
  assert.equal(F.fmtInt(null), '—');
});

check('fmtBytes：B / KB / MB / GB 四档', () => {
  assert.equal(F.fmtBytes(512), '512 B');
  assert.equal(F.fmtBytes(2048), '2.0 KB');
  assert.equal(F.fmtBytes(5 * 1024 * 1024), '5.0 MB');
  assert.equal(F.fmtBytes(2 * 1024 * 1024 * 1024), '2.00 GB');
  assert.equal(F.fmtBytes(0), '0 B');
});

check('fmtDur：秒 / 分秒 / 时分', () => {
  assert.equal(F.fmtDur(45_000), '45 秒');
  assert.equal(F.fmtDur(90_000), '1 分 30 秒');
  assert.equal(F.fmtDur(3 * 3600_000 + 30 * 60_000), '3 小时 30 分');
  assert.equal(F.fmtDur(0), '0 秒');
  assert.equal(F.fmtDur(-1), '0 秒');
});

check('fmtDurShort：给断档时长用的粗粒度（≥1 天就换成天）', () => {
  assert.equal(F.fmtDurShort(90), '2 分');
  assert.equal(F.fmtDurShort(3600), '1.0 小时');
  assert.equal(F.fmtDurShort(5 * 3600), '5.0 小时');
  assert.equal(F.fmtDurShort(49 * 3600), '2.0 天');
  assert.equal(F.fmtDurShort(3 * 86400), '3.0 天');
  assert.equal(F.fmtDurShort(null), '—');
});

check('fmtClock / fmtTs：一个是本地时钟，一个是行情时间（UTC）', () => {
  assert.match(F.fmtClock(Date.UTC(2026, 9, 16, 2, 3, 4)), /^\d{2}:\d{2}:\d{2}$/);
  assert.match(F.fmtTs(1792114800), /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
  assert.equal(F.fmtTs(0), '—');
  assert.equal(F.fmtTs(null), '—');
});

check('fmtPct：一位小数，null 走占位符（不能显示成 0.0%）', () => {
  assert.equal(F.fmtPct(1), '100.0%');
  assert.equal(F.fmtPct(0.98765), '98.8%');
  assert.equal(F.fmtPct(0), '0.0%');
  assert.equal(F.fmtPct(null), '—');
  assert.equal(F.fmtPct(undefined), '—');
});

console.log('月历网格');
check('buildMonthGrid：周一起排，前后补空，长度是 7 的倍数', () => {
  // 2026-10-01 是周四 -> 周一起排时前面空 3 格
  const cells = F.buildMonthGrid('2026-10', new Map(), null);
  assert.equal(cells.length % 7, 0);
  const filled = cells.filter(Boolean);
  assert.equal(filled.length, 31);
  assert.equal(cells.indexOf(filled[0]), 3);
  assert.equal(filled[0].d, 1);
  assert.equal(filled[30].d, 31);
});

check('buildMonthGrid：日期不在数据范围内 -> out-of-range，范围里但没抓 -> absent', () => {
  const range = { fromIso: '2026-10-05', toIso: '2026-10-20' };
  const cells = F.buildMonthGrid('2026-10', new Map(), range).filter(Boolean);
  assert.equal(cells.find((c) => c.iso === '2026-10-03').status, 'out-of-range');
  assert.equal(cells.find((c) => c.iso === '2026-10-10').status, 'absent');
  assert.equal(cells.find((c) => c.iso === '2026-10-25').status, 'out-of-range');
});

check('buildMonthGrid：有数据的日子用库里的记录，并补上日号', () => {
  const byIso = new Map([
    ['2026-10-05', { iso: '2026-10-05', n: 288, expected: 288, missing: 0, status: 'full', states: '1'.repeat(288) }],
  ]);
  const cells = F.buildMonthGrid('2026-10', byIso, null).filter(Boolean);
  const c = cells.find((x) => x.iso === '2026-10-05');
  assert.equal(c.n, 288);
  assert.equal(c.d, 5);
  assert.equal(c.status, 'full');
});

check('buildMonthGrid：非法月份返回空数组而不是抛错', () => {
  assert.deepEqual(F.buildMonthGrid('', new Map(), null), []);
  assert.deepEqual(F.buildMonthGrid(undefined, new Map(), null), []);
});

console.log('日历格子派生量');
check('dayRatio：实有/应有，无参照时按有没有数据给 0 或 100', () => {
  assert.equal(F.dayRatio({ expected: 288, missing: 288, n: 0 }), 0);
  assert.equal(F.dayRatio({ expected: 288, missing: 0, n: 288 }), 100);
  assert.equal(F.dayRatio({ expected: 288, missing: 144, n: 144 }), 50);
  assert.equal(F.dayRatio({ expected: 0, n: 5 }), 100);
  assert.equal(F.dayRatio({ expected: 0, n: 0 }), 0);
  assert.equal(F.dayRatio(null), 0);
});

check('dayStatusText：整日无数据要和"没抓过"区分开', () => {
  assert.equal(F.dayStatusText({ status: 'gap-big', n: 0, missing: 288 }), '整日无数据');
  assert.equal(F.dayStatusText({ status: 'absent', n: 0 }), '未抓取');
  assert.equal(F.dayStatusText({ status: 'out-of-range', n: 0 }), '不在范围');
  assert.equal(F.dayStatusText({ status: 'full', n: 288, missing: 0 }), '完整');
  assert.equal(F.dayStatusText({ status: 'gap', n: 200, missing: 88 }), '缺 88 段');
  assert.equal(F.dayStatusText({ status: 'unknown', n: 10 }), '无参照');
});

console.log('时间轴矩阵布局（纯几何）');
check('computeMatrixLayout：宽度刚好用满可用宽度，格子宽在 1.4–6 之间', () => {
  const l = F.computeMatrixLayout({ rows: 100, slotsPerDay: 288, availW: 1000 });
  assert.equal(l.gutter, 92);
  assert.ok(Math.abs(l.bucketW - 3.125) < 1e-6);
  assert.equal(l.width, 1000);
  // 46(直方图) + 8(间隙) + 100*9 + 20(底部刻度)
  assert.equal(l.height, 46 + 8 + 900 + 20);
  assert.equal(l.matrixTop, 54);
});

check('computeMatrixLayout：太窄时钳到 1.4 并让容器横向滚动', () => {
  const l = F.computeMatrixLayout({ rows: 10, slotsPerDay: 288, availW: 200 });
  assert.equal(l.bucketW, 1.4);
  assert.ok(l.width > 200, '应当超出可用宽度，交给外层滚动');
});

check('computeMatrixLayout：太宽时钳到 6，不会把一天拉得特别长', () => {
  const l = F.computeMatrixLayout({ rows: 10, slotsPerDay: 24, availW: 5000 });
  assert.equal(l.bucketW, 6);
  assert.equal(l.width, 92 + 144 + 8);
});

check('matrixHitTest：命中格子 / 直方图 / 各自边界外返回 null', () => {
  const l = F.computeMatrixLayout({ rows: 100, slotsPerDay: 288, availW: 1000 });
  const at = (x, y) => F.matrixHitTest(x, y, l);

  assert.deepEqual(at(95, l.matrixTop + 5), { kind: 'cell', row: 0, slot: 0 });
  assert.deepEqual(at(95 + 3.2, l.matrixTop + 12), { kind: 'cell', row: 1, slot: 1 });
  // 左侧日期栏留白
  assert.equal(at(50, l.matrixTop + 5), null);
  // 直方图区域
  assert.deepEqual(at(95, 10), { kind: 'hist', slot: 0 });
  // 直方图与矩阵之间的空隙不算命中
  assert.equal(at(95, l.histH + 2), null);
  // 超出最后一格
  assert.equal(at(95 + 288 * l.bucketW + 5, l.matrixTop + 5), null);
  // 超出最后一行
  assert.equal(at(95, l.matrixTop + 100 * l.rowH + 5), null);
});

check('slotState：四态语义与后端 src/calendar.mjs 对齐', () => {
  assert.deepEqual(F.slotState('1'), { k: 'present', text: '有数据', expected: true });
  assert.deepEqual(F.slotState('2'), { k: 'missing', text: '应有但缺', expected: true });
  assert.deepEqual(F.slotState('3'), { k: 'extra', text: '有数据（无参照）', expected: false });
  assert.equal(F.slotState('0').k, 'off');
  assert.equal(F.slotState(undefined).k, 'off');
});

console.log(`\n${passed} 项通过${failed ? `，${failed} 项失败` : '，全部通过'}`);
