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
import { computeCoverage, dayLabel } from '../src/coverage.mjs';
import { openStore, makeWriter, readBars } from '../src/store.mjs';

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

console.log('覆盖度 coverage');
{
  const DAY = 86400;
  const tmp2 = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-cov-'));
  const db2 = path.join(tmp2, 'cov.sqlite');
  const store2 = openStore(db2);
  const w2 = makeWriter(store2);

  const START_DAY = 20000; // 任意起始日，20000 天 = 4 整周
  const WEEKS = 4;
  const HOLE_DAY = START_DAY + 10; // 抽掉这一整天
  const SLOTS = 288; // 24h / 5min

  for (let d = START_DAY; d < START_DAY + WEEKS * 7; d++) {
    if (d === HOLE_DAY) continue;
    for (let i = 0; i < SLOTS; i++) {
      w2.writeBar({
        symbol: 'TEST:SYM',
        series: 'sds_1',
        t: d * DAY + i * 300,
        interval: '5m',
        o: 100,
        h: 101,
        l: 99,
        c: 100.5,
        v: 1,
        source: 'test',
        raw: null,
      });
    }
  }
  w2.close();

  const db3 = openStore(db2);
  const [rep] = computeCoverage(db3, { symbol: 'TEST:SYM' });
  db3.close();

  check('覆盖度：识别出唯一被抽掉的那一天', () => {
    assert.equal(rep.gaps.length, 1);
    assert.equal(dayLabel(rep.gaps[0].day), dayLabel(HOLE_DAY));
  });
  check('覆盖度：缺失量 = 288 个时段（整日）', () => {
    assert.equal(rep.gaps[0].missing.length, SLOTS);
    // expectedTotal 统计的是"整个区间应有"（含被抽掉那天的 288 个时段），
    // missingTotal 才是真正的缺口
    assert.equal(rep.expectedTotal, WEEKS * 7 * SLOTS);
    assert.equal(rep.matched, WEEKS * 7 * SLOTS - SLOTS);
    assert.equal(rep.missingTotal, SLOTS);
    assert.equal(rep.coverageRatio, 1 - SLOTS / (WEEKS * 7 * SLOTS));
  });
  check('覆盖度：整周样本 -> 参照充分，不做"偏乐观"告警', () => {
    assert.equal(rep.patternWeak, false);
    assert.equal(rep.unknownDays.length, 0);
  });
  check('覆盖度：断档列表交叉验证到同一处', () => {
    assert.equal(rep.breaks.length, 1);
    assert.equal(rep.breaks[0].missing, SLOTS);
  });
  check('覆盖度：只有 1 天样本时判定参照不足', () => {
    const tmp3 = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-cov1-'));
    const db4 = path.join(tmp3, 'one.sqlite');
    const s4 = openStore(db4);
    const w4 = makeWriter(s4);
    for (let i = 0; i < SLOTS; i++) {
      w4.writeBar({
        symbol: 'TEST:ONE', series: 'sds_1', t: START_DAY * DAY + i * 300, interval: '5m',
        o: 1, h: 1, l: 1, c: 1, v: 1, source: 'test', raw: null,
      });
    }
    w4.close();
    const db5 = openStore(db4);
    const [r1] = computeCoverage(db5, { symbol: 'TEST:ONE' });
    db5.close();
    assert.equal(r1.patternWeak, true);
  });

  fs.rmSync(tmp2, { recursive: true, force: true });
}

console.log('导出收敛 readBars');
{
  const tmp6 = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-dedupe-'));
  const db6 = path.join(tmp6, 'd.sqlite');
  const s6 = openStore(db6);
  const w6 = makeWriter(s6);
  const mk = (series, t, c, seenAt) => ({
    symbol: 'TEST:S', series, t, interval: '5m', o: 1, h: c, l: 1, c, v: 1, source: 't', raw: null, seen_at: seenAt,
  });
  // 同一根 K 线来自两条连接（重连换了 series 号），后者更新
  w6.writeBar(mk('sds_1', 300000, 10, 1000));
  w6.writeBar(mk('sds_2', 300000, 11, 2000));
  w6.writeBar(mk('sds_1', 300300, 20, 1000));
  w6.writeBar({ ...mk('sds_1', 900000, 99, 1000), interval: '15m' }); // 不同周期不该被合并
  w6.close();

  const db7 = openStore(db6);
  const d = readBars(db7, { symbol: 'TEST:S', dedupe: true });
  const raw = readBars(db7, { symbol: 'TEST:S', dedupe: false });
  db7.close();

  check('收敛：同一 (symbol,interval,t) 的重复行被合并', () => {
    assert.equal(raw.rows.length, 4);
    assert.equal(d.rows.length, 3);
    assert.equal(d.collapsed, 1);
  });
  check('收敛：保留的是最后见到的那一行', () => {
    const row = d.rows.find((r) => r.t === 300000);
    assert.equal(row.c, 11);
  });
  check('收敛：不同周期不互相覆盖', () => {
    assert.equal(d.rows.filter((r) => r.t === 900000).length, 1);
  });

  fs.rmSync(tmp6, { recursive: true, force: true });
}

fs.rmSync(tmp, { recursive: true, force: true });

console.log(`\n${passed} 项通过${process.exitCode ? '（有失败项）' : '，全部通过'}`);
