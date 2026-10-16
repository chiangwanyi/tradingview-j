// 离线自测：用合成帧跑通「切帧 -> 抽 K 线 -> 入库 -> 导出」
// 目的：把不含 Chrome 的部分先验证掉，等调试开关打开只剩"接上去"这一件不确定的事。
//
// 运行：node test/selftest.mjs

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import { splitFrame, buildFrame, extractBars, toBar, symbolFromUrl, intervalName } from '../src/frames.mjs';
import { ingest } from '../src/ingest.mjs';
import { openStore, makeWriter } from '../src/store.mjs';

let passed = 0;
function check(name, fn) {
  try {
    fn();
    console.log(`  ok   ${name}`);
    passed++;
  } catch (e) {
    console.log(`  FAIL ${name}\n       ${e.message}`);
    process.exitCode = 1;
  }
}

const T0 = 1760000000;

console.log('切帧 splitFrame');
check('多段串接：心跳 + JSON 各归各位', () => {
  const text =
    buildFrame({ m: 'heartbeat', p: [] }) + '~h~7' + buildFrame({ m: 'du', p: [{ sds_1: { s: [] } }] });
  const segs = splitFrame(text);
  assert.equal(segs.length, 3);
  assert.equal(segs[0].kind, 'json');
  assert.equal(segs[0].value.m, 'heartbeat');
  assert.equal(segs[1].kind, 'heartbeat');
  assert.equal(segs[2].kind, 'json');
  assert.equal(segs[2].value.m, 'du');
});

check('裸心跳整帧', () => {
  const segs = splitFrame('~h~23');
  assert.equal(segs.length, 1);
  assert.equal(segs[0].kind, 'heartbeat');
});

check('负载长度与声明长度一致', () => {
  const text = buildFrame({ m: 'x', p: [1, 2, 3] });
  const segs = splitFrame(text);
  assert.equal(segs[0].length, segs[0].raw.length);
});

check('声明长度对不上时退化为切到下一个帧头', () => {
  // 人为把声明长度写错，模拟按字节 / 按字符的语义差异
  const bad = '~m~999~m~{"m":"du","p":[]}' + buildFrame({ m: 'tail', p: [] });
  const segs = splitFrame(bad);
  assert.equal(segs.length, 2);
  assert.equal(segs[0].value.m, 'du');
  assert.equal(segs[1].value.m, 'tail');
});

check('负载里含中文时仍能切出正确边界', () => {
  const text = buildFrame({ m: 'x', p: ['中文字符'] }) + buildFrame({ m: 'y', p: [] });
  const segs = splitFrame(text);
  assert.equal(segs.length, 2);
  assert.equal(segs[1].value.m, 'y');
});

console.log('抽 K 线 extractBars / toBar');
check('timescale_update 形状', () => {
  const msg = {
    m: 'timescale_update',
    p: [
      {
        sds_1: {
          s: [
            { i: 0, v: [T0, 20512.5, 20515, 20510.25, 20514.75, 312] },
            { i: 1, v: [T0 + 60, 20514.75, 20518, 20513, 20516.5, 208] },
          ],
          t: 's1',
          lb: 2,
        },
      },
    ],
  };
  const bars = extractBars(msg).map((b) => toBar(b, 'CME_MINI:MNQ1!'));
  assert.equal(bars.length, 2);
  assert.equal(bars[0].series, 'sds_1');
  assert.equal(bars[0].t, T0);
  assert.equal(bars[0].o, 20512.5);
  assert.equal(bars[0].c, 20514.75);
  assert.equal(bars[0].v, 312);
  assert.equal(bars[1].t, T0 + 60);
});

check('du 增量与嵌套 ns 不误抽', () => {
  const msg = {
    m: 'du',
    p: [{ sds_1: { s: [{ i: 9, v: [T0 + 120, 20516.5, 20520, 20516, 20519, 150] }], ns: { d: [T0 + 120] } } }],
  };
  const bars = extractBars(msg).map((b) => toBar(b));
  assert.equal(bars.length, 1);
  assert.equal(bars[0].series, 'sds_1');
  assert.equal(bars[0].c, 20519);
});

check('无成交量（5 元素）也能抽', () => {
  const msg = { m: 'du', p: [{ sds_7: { s: [{ i: 0, v: [T0, 1, 2, 0.5, 1.5] }] } }] };
  const bars = extractBars(msg).map((b) => toBar(b));
  assert.equal(bars.length, 1);
  assert.equal(bars[0].series, 'sds_7');
  assert.equal(bars[0].v, null);
});

check('空 s 数组不产生 K 线', () => {
  assert.equal(extractBars({ m: 'du', p: [{ sds_1: { s: [] } }] }).length, 0);
});

check('非法数值被丢弃', () => {
  assert.equal(toBar({ series: 'x', v: ['a', 1, 2, 3, 4] }), null);
  assert.equal(toBar({ series: 'x', v: [T0, 1] }), null);
});

check('从 chart URL 读回品种', () => {
  assert.equal(
    symbolFromUrl('https://cn.tradingview.com/chart/EuR5QG29/?symbol=CME_MINI%3AMNQ1%21'),
    'CME_MINI:MNQ1!',
  );
  assert.equal(symbolFromUrl('https://cn.tradingview.com/chart/x/'), null);
});

console.log('真实样本形状（2026-10-16 实抓）');
check('常量 bar_close_time 不该被当成每根 K 线的收盘时间', () => {
  // 实抓到的形状：lbs 挂在 series 容器上，整段抓包里 bar_close_time 只有一个取值，
  // 它是图表右侧可视边界，不是该 K 线的收盘时间。
  // 曾经拿它减 t 当周期，得到 94200s / 15m 这种随时间漂移的垃圾值。这里锁死这个坑。
  const bct = 1792115700;
  const msg = {
    m: 'du',
    p: [
      'cs_gaSTXIAFy41H',
      {
        sds_1: {
          s: [
            { i: 299, v: [1792114500, 30063.75, 30071, 30052.75, 30061.25, 3772] },
            { i: 300, v: [1792114800, 30067.25, 30067.25, 30056, 30060.75, 843] },
          ],
          ns: { d: '', indexes: 'nochange' },
          t: 's1',
          lbs: { bar_close_time: bct },
        },
      },
    ],
  };
  const bars = extractBars(msg).map((b) => toBar(b, 'CME_MINI:MNQ1!'));
  assert.equal(bars.length, 2);
  // 两根 K 线必须得到同一个结论（都没有 per-bar 周期），而不是差 300 秒
  assert.equal(bars[0].interval, undefined);
  assert.equal(bars[1].interval, undefined);
  assert.equal(bars[0].timescale, 's1');
});

check('相邻 300 秒 -> 5m（这才是真实周期）', () => {
  const deltas = [1792114800 - 1792114500];
  assert.equal(intervalName(deltas[0]), '5m');
});

check('st1 指标序列（st 为空 + ns.d 是巨大字符串）不产生 K 线', () => {
  const msg = {
    m: 'du',
    p: [
      'cs_gaSTXIAFy41H',
      { st1: { st: [], ns: { d: '{"graphicsCmds":{"create":{"backgrounds":[{"data":[{"id":2,"start":0}]}]}}}' } } },
    ],
  };
  assert.equal(extractBars(msg).length, 0);
});

check('st 数组里带 v 的指标值也不被当成 K 线', () => {
  const msg = { m: 'du', p: [{ st1: { st: [{ i: 0, v: [1792114800, 12.5] }], ns: {} } }] };
  assert.equal(extractBars(msg).length, 0);
});

console.log('入库 -> 导出');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-selftest-'));
const ndjson = path.join(tmp, 'sample.ndjson');
{
  const lines = [
    JSON.stringify({
      k: 'meta',
      at: Date.now(),
      target: { url: 'https://cn.tradingview.com/chart/EuR5QG29/?symbol=CME_MINI%3AMNQ1%21' },
      symbolHint: 'CME_MINI:MNQ1!',
    }),
    JSON.stringify({
      k: 'frame',
      at: Date.now(),
      dir: 'recv',
      opcode: 1,
      text:
        buildFrame({ m: 'symbol_resolved', p: [{ series_id: 'sds_1', symbol: 'CME_MINI:MNQ1!' }] }) +
        buildFrame({
          m: 'timescale_update',
          p: [
            {
              sds_1: {
                s: [
                  { i: 0, v: [T0, 100, 102, 99, 101, 10] },
                  { i: 1, v: [T0 + 60, 101, 103, 100, 102, 20] },
                  { i: 2, v: [T0 + 120, 102, 105, 101, 103, 30] },
                ],
              },
            },
          ],
        }),
    }),
    JSON.stringify({ k: 'frame', at: Date.now(), dir: 'recv', opcode: 1, text: '~h~11' }),
    // 同一根 K 线的增量更新：收盘价被打到 104，成交量变大，应覆盖而不是新增
    JSON.stringify({
      k: 'frame',
      at: Date.now(),
      dir: 'recv',
      opcode: 1,
      text: buildFrame({
        m: 'du',
        p: [{ sds_1: { s: [{ i: 1, v: [T0 + 60, 101, 104, 100, 104, 55] }] } }],
      }),
    }),
    JSON.stringify({ k: 'frame', at: Date.now(), dir: 'send', opcode: 1, text: buildFrame({ m: 'heartbeat', p: [] }) }),
  ];
  fs.writeFileSync(ndjson, lines.join('\n') + '\n');
}

const dbFile = path.join(tmp, 'bars.sqlite');
let summary;
await ingest({ files: [ndjson], dbFile, log: () => {} });

const db = openStore(dbFile);
const w = makeWriter(db);
summary = w.stats();

check('幂等：3 根 K 线 + 1 次增量覆盖 = 3 行', () => {
  assert.equal(w.rows({ limit: 0 }).length, 3);
});

check('增量覆盖生效：第 2 根收盘 104 / 量 55', () => {
  const rows = w.rows({ limit: 0 });
  assert.equal(rows[1].t, T0 + 60);
  assert.equal(rows[1].c, 104);
  assert.equal(rows[1].v, 55);
});

check('周期反推：相邻 60 秒 -> 1m', () => {
  const meta = w.listSeries();
  assert.equal(meta.length, 1);
  assert.equal(meta[0].symbol, 'CME_MINI:MNQ1!');
  assert.equal(meta[0].series, 'sds_1');
  assert.equal(meta[0].interval, '1m');
  assert.equal(meta[0].bars, 3);
});

check('品种来自 symbol_resolved 而不是兜底 hint', () => {
  assert.equal(w.rows({ limit: 0 })[0].symbol, 'CME_MINI:MNQ1!');
});

db.close();
fs.rmSync(tmp, { recursive: true, force: true });

console.log(`\n${passed} 项通过${process.exitCode ? '（有失败项）' : '，全部通过'}`);
