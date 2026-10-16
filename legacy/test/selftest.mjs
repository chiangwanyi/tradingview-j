// 离线自测：不碰 Chrome，把能离线验证的部分全部钉住。
//
// 分五段：
//   1. 协议层（切帧 / 抽 K 线）—— 纯函数
//   2. 存储层（一品种一库 / 索引 / 幂等 / 导出收敛）
//   3. 入库路由（多品种分流到各自文件）
//   4. 覆盖度与日历（缺口识别 / 逐格状态位图 / 直方图）
//   5. 迁移（旧混合库 -> 分品种库）
//
// 运行：node test/selftest.mjs

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';

import { splitFrame, buildFrame, extractBars, toBar, symbolFromUrl, intervalName } from '../src/frames.mjs';
import { runIngest, createIngestor } from '../src/ingest.mjs';
import { computeCoverage, dayLabel } from '../src/coverage.mjs';
import { buildCalendar, bucketSeconds, daySegments } from '../src/calendar.mjs';
import { openStore, openSymbolStore, makeWriter, readBars, summarize, getMeta } from '../src/store.mjs';
import { symbolSlug, fileForSymbol, loadIndex, listDbFiles, symbolForSlug } from '../src/registry.mjs';
import { migrateLegacy } from '../src/migrate.mjs';
import { rawDirFor, rememberRawFile, listRawFiles, reconcileRawIndex } from '../src/rawindex.mjs';
import { calendarFor } from '../server/views.mjs';

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

const T0 = 1760000000;
const DAY = 86400;

// ============================================================ 1. 协议层

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
  const segs = splitFrame(buildFrame({ m: 'x', p: [1, 2, 3] }));
  assert.equal(segs[0].length, segs[0].raw.length);
});

check('声明长度对不上时退化为切到下一个帧头', () => {
  const bad = '~m~999~m~{"m":"du","p":[]}' + buildFrame({ m: 'tail', p: [] });
  const segs = splitFrame(bad);
  assert.equal(segs.length, 2);
  assert.equal(segs[0].value.m, 'du');
  assert.equal(segs[1].value.m, 'tail');
});

check('负载里含中文时仍能切出正确边界', () => {
  const segs = splitFrame(buildFrame({ m: 'x', p: ['中文字符'] }) + buildFrame({ m: 'y', p: [] }));
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
  assert.equal(bars[0].v, 312);
  assert.equal(bars[1].t, T0 + 60);
});

check('du 增量与嵌套 ns 不误抽', () => {
  const bars = extractBars({
    m: 'du',
    p: [{ sds_1: { s: [{ i: 9, v: [T0 + 120, 20516.5, 20520, 20516, 20519, 150] }], ns: { d: [T0 + 120] } } }],
  }).map((b) => toBar(b));
  assert.equal(bars.length, 1);
  assert.equal(bars[0].c, 20519);
});

check('无成交量（5 元素）也能抽', () => {
  const bars = extractBars({ m: 'du', p: [{ sds_7: { s: [{ i: 0, v: [T0, 1, 2, 0.5, 1.5] }] } }] }).map((b) =>
    toBar(b),
  );
  assert.equal(bars.length, 1);
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
          lbs: { bar_close_time: 1792115700 },
        },
      },
    ],
  };
  const bars = extractBars(msg).map((b) => toBar(b, 'CME_MINI:MNQ1!'));
  assert.equal(bars.length, 2);
  assert.equal(bars[0].interval, undefined);
  assert.equal(bars[1].interval, undefined);
  assert.equal(bars[0].timescale, 's1');
});

check('相邻 300 秒 -> 5m（这才是真实周期）', () => {
  assert.equal(intervalName(1792114800 - 1792114500), '5m');
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
  assert.equal(extractBars({ m: 'du', p: [{ st1: { st: [{ i: 0, v: [1792114800, 12.5] }], ns: {} } }] }).length, 0);
});

// ============================================================ 2. 存储层

console.log('存储层 registry / store');
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-root-'));

check('slug：把 Windows 不允许的字符换掉，并去掉结尾的点/空格', () => {
  assert.equal(symbolSlug('CME_MINI:MNQ1!'), 'CME_MINI_MNQ1!');
  assert.equal(symbolSlug('A/B\\C*D?E'), 'A_B_C_D_E');
  assert.equal(symbolSlug('abc.'), 'abc');
  assert.equal(symbolSlug(''), 'unknown');
});

check('slug 撞车时加短哈希，两个品种绝不共用一个文件', () => {
  const a = fileForSymbol(root, 'X:Y');
  const b = fileForSymbol(root, 'X/Y'); // 归一后 slug 相同
  assert.notEqual(a, b);
  assert.notEqual(path.basename(a).toLowerCase(), path.basename(b).toLowerCase());
  const idx = loadIndex(root);
  assert.notEqual(idx.symbols['X:Y'].file, idx.symbols['X/Y'].file);
});

check('索引可反查，且同一品种文件名稳定', () => {
  const f1 = fileForSymbol(root, 'CME_MINI:MNQ1!');
  const f2 = fileForSymbol(root, 'CME_MINI:MNQ1!');
  assert.equal(f1, f2);
  assert.equal(symbolForSlug(root, 'CME_MINI_MNQ1!'), 'CME_MINI:MNQ1!');
  assert.equal(symbolForSlug(root, '不存在的slug'), null);
});

check('一个库只服务一个品种：writer 会把 symbol 钉死', () => {
  const { db, file } = openSymbolStore(root, 'AAA:BBB');
  assert.equal(getMeta(db, 'symbol'), 'AAA:BBB');
  const w = makeWriter(db, { symbol: 'AAA:BBB' });
  // 故意传别的 symbol，应当被覆盖
  w.writeBar({ symbol: 'OTHER', series: 'sds_1', t: T0, interval: '5m', o: 1, h: 1, l: 1, c: 1, v: 1 });
  w.flush();
  const rows = w.rows({ limit: 0 });
  assert.equal(rows.length, 1);
  assert.equal(rows[0].symbol, 'AAA:BBB');
  db.close();
  assert.ok(fs.existsSync(file));
});

check('listDbFiles 能认出磁盘上但不在索引里的库文件', () => {
  // 模拟"别人拷过来一个库"
  const dir = path.join(root, 'data', 'db');
  fs.mkdirSync(dir, { recursive: true });
  const stray = path.join(dir, 'STRAY_SYM.sqlite');
  const sdb = openStore(stray, { symbol: 'STRAY:SYM' });
  sdb.close();
  const found = listDbFiles(root).find((f) => f.filename === 'STRAY_SYM.sqlite');
  assert.ok(found);
  assert.equal(found.indexed, false);
});

check('导出收敛：同一 (symbol,interval,t) 的重复行被合并，保留最后见到的', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-dedupe-'));
  const db = openStore(path.join(tmp, 'd.sqlite'));
  const w = makeWriter(db);
  const mk = (series, t, c, seenAt, interval = '5m') => ({
    symbol: 'TEST:S', series, t, interval, o: 1, h: c, l: 1, c, v: 1, source: 't', raw: null, seen_at: seenAt,
  });
  w.writeBar(mk('sds_1', 300000, 10, 1000));
  w.writeBar(mk('sds_2', 300000, 11, 2000)); // 重连换了 series 号，是同一根 K 线
  w.writeBar(mk('sds_1', 300300, 20, 1000));
  w.writeBar(mk('sds_1', 900000, 99, 1000, '15m')); // 不同周期不该被合并
  w.close();

  const db2 = openStore(path.join(tmp, 'd.sqlite'));
  const deduped = readBars(db2, { symbol: 'TEST:S', dedupe: true });
  const raw = readBars(db2, { symbol: 'TEST:S', dedupe: false });
  const sum = summarize(db2);
  db2.close();

  assert.equal(raw.rows.length, 4);
  assert.equal(deduped.rows.length, 3);
  assert.equal(deduped.collapsed, 1);
  assert.equal(deduped.rows.find((r) => r.t === 300000).c, 11);
  assert.equal(deduped.rows.filter((r) => r.t === 900000).length, 1);
  // summarize 报的"根数"必须是去重后的，否则 UI 上的数字会虚高
  assert.equal(sum.bars, 3);
  assert.equal(sum.rows, 4);
  // 重连换号要能被看见
  assert.equal(sum.seriesCount, 2);
  fs.rmSync(tmp, { recursive: true, force: true });
});

// ============================================================ 3. 入库路由

console.log('入库路由（多品种分流到各自库文件）');
{
  const r2 = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-ingest-'));
  const mkFrame = (m, bars) =>
    JSON.stringify({
      k: 'frame',
      at: Date.now(),
      dir: 'recv',
      opcode: 1,
      text: buildFrame({ m, p: bars.map((b, i) => ({ sds_1: { s: [{ i, v: b }] } })) }),
    });

  // 两个品种，各三条 K 线，混在同一个抓包文件里
  const lines = [
    JSON.stringify({ k: 'meta', at: Date.now(), symbolHint: 'CME_MINI:MNQ1!' }),
    mkFrame('timescale_update', [
      [T0, 100, 102, 99, 101, 10],
      [T0 + 300, 101, 103, 100, 102, 20],
      [T0 + 600, 102, 105, 101, 103, 30],
    ]),
    JSON.stringify({ k: 'frame', at: Date.now(), dir: 'recv', opcode: 1, text: '~h~11' }),
    // 同一根 K 线的增量更新：收盘价被打到 104，应覆盖而不是新增
    JSON.stringify({
      k: 'frame',
      at: Date.now(),
      dir: 'recv',
      opcode: 1,
      text: buildFrame({ m: 'du', p: [{ sds_1: { s: [{ i: 1, v: [T0 + 300, 101, 104, 100, 104, 55] }] } }] }),
    }),
    JSON.stringify({
      k: 'frame',
      at: Date.now(),
      dir: 'recv',
      opcode: 1,
      text: buildFrame({
        m: 'du',
        p: [{ sds_1: { s: [{ i: 0, v: [T0, 7, 9, 6, 8, 1] }], symbol: 'CME_MINI:ESZ6', series_id: 'sds_1' } }],
      }),
    }),
  ];
  const file = path.join(r2, 'sample.ndjson');
  fs.writeFileSync(file, lines.join('\n') + '\n');

  // 用一个能区分品种的写法：直接喂 createIngestor，显式给 symbolHint
  const ing = createIngestor({ root: r2 });
  for (const line of lines) ing.push(JSON.parse(line));
  ing.flush();
  const openAfter = ing.openFiles();
  const bySymbol = Object.fromEntries([...ing.summary.bySymbol].map(([k, v]) => [k, v.bars]));
  ing.close();

  check('抓包里的 K 线按 series 解析结果分流到各品种库文件', () => {
    // summary.bars 记的是"写入次数"而不是"最终根数"：
    // timescale_update 写 3 根、du 覆盖其中 1 根，所以 MNQ1 记 4 次，但库里只有 3 行。
    assert.equal(bySymbol['CME_MINI:MNQ1!'], 4);
    // 最后一帧带了 symbol_resolved 类的字段（series_id + symbol），
    // 于是 sds_1 被重新解析到 ESZ6，那一根归它
    assert.equal(bySymbol['CME_MINI:ESZ6'], 1);
    const f = openAfter.find((x) => x.symbol === 'CME_MINI:MNQ1!');
    assert.ok(f, '应当开了 CME_MINI:MNQ1! 的库');
    assert.ok(fs.existsSync(f.file));
    assert.ok(f.file.endsWith('CME_MINI_MNQ1!.sqlite'));
    const es = openAfter.find((x) => x.symbol === 'CME_MINI:ESZ6');
    assert.ok(es, '应当开了 CME_MINI:ESZ6 的库');
    assert.ok(!es.file.endsWith('CME_MINI_MNQ1!.sqlite'), '两个品种不能写进同一个文件');
  });

  check('每个品种库只装自己的 K 线', () => {
    const a = openStore(fileForSymbol(r2, 'CME_MINI:MNQ1!'));
    assert.equal(a.prepare('SELECT COUNT(*) n FROM bars').get().n, 3);
    a.close();
    const b = openStore(fileForSymbol(r2, 'CME_MINI:ESZ6'));
    assert.equal(b.prepare('SELECT COUNT(*) n FROM bars').get().n, 1);
    b.close();
  });

  check('幂等：3 根 K 线 + 1 次增量覆盖 = 3 根', () => {
    const db = openStore(fileForSymbol(r2, 'CME_MINI:MNQ1!'));
    const w = makeWriter(db);
    assert.equal(w.rows({ limit: 0 }).length, 3);
    assert.equal(w.stats().bars, 3);
    db.close();
  });

  check('增量覆盖生效：第 2 根收盘 104 / 量 55', () => {
    const db = openStore(fileForSymbol(r2, 'CME_MINI:MNQ1!'));
    const w = makeWriter(db);
    const rows = w.rows({ limit: 0 });
    assert.equal(rows[1].t, T0 + 300);
    assert.equal(rows[1].c, 104);
    assert.equal(rows[1].v, 55);
    db.close();
  });

  check('周期反推：相邻 300 秒 -> 5m', () => {
    const db = openStore(fileForSymbol(r2, 'CME_MINI:MNQ1!'));
    const meta = makeWriter(db).listSeries();
    assert.equal(meta[0].interval, '5m');
    db.close();
  });

  check('抓包文件按品种分目录 + 索引可回填', () => {
    const dir = rawDirFor(r2, 'CME_MINI:MNQ1!');
    assert.ok(dir.includes('CME_MINI_MNQ1!'));
    fs.mkdirSync(dir, { recursive: true });
    const f = path.join(dir, 'x.ndjson');
    fs.writeFileSync(f, JSON.stringify({ k: 'meta', at: 1, symbolHint: 'CME_MINI:MNQ1!' }) + '\n');
    rememberRawFile(r2, { file: f, symbol: 'CME_MINI:MNQ1!' });
    const listed = listRawFiles(r2);
    assert.equal(listed.length, 1);
    assert.equal(listed[0].symbol, 'CME_MINI:MNQ1!');
    // 未登记的旧文件能被回填
    fs.writeFileSync(path.join(r2, 'data', 'raw', 'old.ndjson'), JSON.stringify({ k: 'meta', at: 1, symbolHint: 'OLD:SYM' }) + '\n');
    const r = reconcileRawIndex(r2);
    assert.ok(r.updated >= 1);
    assert.equal(listRawFiles(r2).find((x) => x.name === 'old.ndjson').symbol, 'OLD:SYM');
  });

  // runIngest 是 async 的，check() 是同步的 —— 单独在模块顶层 await，
  // 免得错误被吞在没人 await 的 promise 里（那是最难查的一类"测试永远绿"）。
  {
    const rr = await runIngest({ root: r2, files: [file], log: () => {} });
    check('runIngest 批处理：from-file 的解析结果与流式一致', () => {
      assert.equal(rr.summary.bars, 5);
      assert.equal(rr.bySymbol['CME_MINI:MNQ1!'], 4);
      assert.equal(rr.bySymbol['CME_MINI:ESZ6'], 1);
    });
  }

  fs.rmSync(r2, { recursive: true, force: true });
}

// ============================================================ 4. 覆盖度与日历

console.log('覆盖度 coverage');
{
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-cov-'));
  const dbFile = path.join(tmp, 'cov.sqlite');
  const db = openStore(dbFile, { symbol: 'TEST:SYM' });
  const w = makeWriter(db, { symbol: 'TEST:SYM' });

  const START_DAY = 20000; // 20000 天 = 4 整周
  const WEEKS = 4;
  const HOLE_DAY = START_DAY + 10; // 抽掉这一整天
  const SLOTS = 288; // 24h / 5min

  for (let d = START_DAY; d < START_DAY + WEEKS * 7; d++) {
    if (d === HOLE_DAY) continue;
    for (let i = 0; i < SLOTS; i++) {
      w.writeBar({
        symbol: 'TEST:SYM', series: 'sds_1', t: d * DAY + i * 300, interval: '5m',
        o: 100, h: 101, l: 99, c: 100.5, v: 1, source: 'test', raw: null,
      });
    }
  }
  w.close();

  const db2 = openStore(dbFile);
  const [rep] = computeCoverage(db2, { symbol: 'TEST:SYM' });

  check('覆盖度：识别出唯一被抽掉的那一天', () => {
    assert.equal(rep.gaps.length, 1);
    assert.equal(dayLabel(rep.gaps[0].day), dayLabel(HOLE_DAY));
  });
  check('覆盖度：缺失量 = 288 个时段（整日）', () => {
    assert.equal(rep.gaps[0].missing.length, SLOTS);
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
  check('覆盖度：bars 是去重后的根数，不是行数', () => {
    assert.equal(rep.bars, WEEKS * 7 * SLOTS - SLOTS);
  });

  console.log('日历与时间轴 buildCalendar');
  const cal = buildCalendar(db2, {});
  check('日历：5m 周期 -> 每天 288 格', () => {
    assert.equal(cal.bucketSec, 300);
    assert.equal(cal.slotsPerDay, 288);
    assert.equal(cal.bucket, '5m');
    assert.equal(cal.days.length, WEEKS * 7);
  });
  check('日历：每一天的 states 长度恒等于 slotsPerDay', () => {
    for (const d of cal.days) assert.equal(d.states.length, 288);
  });
  check('日历：完整日全是 "1"，被抽掉那天全是 "2"', () => {
    const full = cal.days.find((d) => d.iso === dayLabel(START_DAY + 1));
    assert.equal(full.states, '1'.repeat(288));
    const hole = cal.days.find((d) => d.iso === dayLabel(HOLE_DAY));
    assert.equal(hole.states, '2'.repeat(288));
    assert.equal(hole.n, 0);
    assert.equal(hole.status, 'gap-big');
  });
  check('日历：直方图每个时段都应到 28 天里的 27 天', () => {
    assert.equal(cal.histogram.length, 288);
    for (const h of cal.histogram) {
      assert.equal(h.expect, WEEKS * 7);
      assert.equal(h.days, WEEKS * 7 - 1);
    }
  });
  check('日历：月历按月份聚合', () => {
    assert.ok(cal.months.length >= 1);
    const m = cal.months[0];
    assert.equal(m.days.length + cal.months.slice(1).reduce((a, x) => a + x.days.length, 0), WEEKS * 7);
    assert.equal(m.missing + cal.months.slice(1).reduce((a, x) => a + x.missing, 0), SLOTS);
  });
  check('日历：单日明细合并成连续区间', () => {
    const full = cal.days.find((d) => d.iso === dayLabel(START_DAY + 1));
    const segs = daySegments(full, 300);
    assert.equal(segs.length, 1);
    assert.equal(segs[0].label, '00:00–24:00');
  });
  check('日历：桶长度阶梯（1m -> 60s，15m -> 900s，1d -> 86400s）', () => {
    assert.equal(bucketSeconds(60), 60);
    assert.equal(bucketSeconds(900), 900);
    assert.equal(bucketSeconds(86400), 86400);
    // 非标准周期向上取到最近的阶梯
    assert.equal(bucketSeconds(301), 600);
  });
  db2.close();

  check('覆盖度：只有 1 天样本时判定参照不足', () => {
    const tmp3 = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-cov1-'));
    const db4 = path.join(tmp3, 'one.sqlite');
    const s4 = openStore(db4, { symbol: 'TEST:ONE' });
    const w4 = makeWriter(s4, { symbol: 'TEST:ONE' });
    for (let i = 0; i < SLOTS; i++) {
      w4.writeBar({
        symbol: 'TEST:ONE', series: 'sds_1', t: START_DAY * DAY + i * 300, interval: '5m',
        o: 1, h: 1, l: 1, c: 1, v: 1, source: 'test', raw: null,
      });
    }
    w4.close();
    const db5 = openStore(db4);
    const [r1] = computeCoverage(db5, { symbol: 'TEST:ONE' });
    assert.equal(r1.patternWeak, true);
    db5.close();
    fs.rmSync(tmp3, { recursive: true, force: true });
  });

  fs.rmSync(tmp, { recursive: true, force: true });
}

// ============================================================ 4b. 视图层：单日 focus 的语义

console.log('视图层 calendarFor 的单日 focus');
{
  // 回归用例：月历会把整月都画成格子（数据没覆盖到的日子也画），
  // 所以前端传上来的 ?day= 完全可能落在 cal.days 之外。
  // 一度是这时候 focus 留 null，页面就出现「标题说选中了某天、正文还是占位文案」的自相矛盾。
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-view-'));
  const SYM = 'TEST:VIEW!';
  const DAY0 = 21000;
  const { db, file } = openSymbolStore(root, SYM);
  const w = makeWriter(db, { symbol: SYM });
  for (let i = 0; i < 288; i++) {
    w.writeBar({
      symbol: SYM, series: 'sds_1', t: DAY0 * DAY + i * 300, interval: '5m',
      o: 1, h: 1, l: 1, c: 1, v: 1, source: 'test', raw: null,
    });
  }
  w.close();

  const slug = symbolSlug(SYM);
  const far = calendarFor(root, slug, { day: '1999-01-01' });
  check('视图层：范围外的日子也给 focus（status=out-of-range），不是 null', () => {
    assert.ok(far.focus, 'focus 为 null —— 前端只能显示占位文案');
    assert.equal(far.focus.iso, '1999-01-01');
    assert.equal(far.focus.status, 'out-of-range');
    assert.equal(far.focus.n, 0);
    assert.deepEqual(far.focus.segments, []);
  });

  const iso0 = dayLabel(DAY0);
  const hit = calendarFor(root, slug, { day: iso0 });
  check('视图层：范围内的日子给根数与连续时段', () => {
    assert.ok(hit.focus);
    assert.equal(hit.focus.iso, iso0);
    assert.equal(hit.focus.n, 288);
    assert.equal(hit.focus.segments.length, 1);
    assert.equal(hit.focus.segments[0], '00:00–24:00');
  });

  const malformed = calendarFor(root, slug, { day: '乱写的' });
  check('视图层：day 参数不成形时不去硬凑 focus（宁可没有）', () => {
    assert.equal(malformed.focus, undefined);
  });

  check('视图层：库文件落在 data/db/<slug>.sqlite', () => {
    assert.equal(path.basename(file), `${slug}.sqlite`);
    assert.ok(file.includes(path.join('db')), file);
  });

  fs.rmSync(root, { recursive: true, force: true });
}

// ============================================================ 5. 迁移

console.log('迁移 legacy -> 分品种库');
{
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-mig-'));
  // 造一个旧式混合库：两条品种混在一张 bars 表里
  fs.mkdirSync(path.join(tmp, 'data'), { recursive: true });
  const legacy = path.join(tmp, 'data', 'bars.sqlite');
  const ldb = new DatabaseSync(legacy);
  ldb.exec(`
    CREATE TABLE bars (
      symbol TEXT NOT NULL, series TEXT NOT NULL, t INTEGER NOT NULL, interval TEXT,
      timescale TEXT, o REAL, h REAL, l REAL, c REAL, v REAL, source TEXT, raw TEXT,
      seen_at INTEGER NOT NULL, PRIMARY KEY (symbol, series, t));
  `);
  const ins = ldb.prepare('INSERT INTO bars VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)');
  for (let i = 0; i < 10; i++) ins.run('AA:X', 'sds_1', T0 + i * 300, '5m', null, 1, 2, 0.5, 1.5, 3, 'du', null, 1);
  for (let i = 0; i < 7; i++) ins.run('BB:Y', 'sds_1', T0 + i * 60, '1m', null, 1, 2, 0.5, 1.5, 3, 'du', null, 1);
  ldb.close();

  const res = migrateLegacy(tmp, { log: () => {} });
  check('迁移：两个品种各自成库，行数对得上', () => {
    assert.equal(res.migrated, true);
    assert.equal(res.symbols.length, 2);
    const da = openStore(fileForSymbol(tmp, 'AA:X'));
    assert.equal(da.prepare('SELECT COUNT(*) n FROM bars').get().n, 10);
    assert.equal(getMeta(da, 'symbol'), 'AA:X');
    da.close();
    const dbb = openStore(fileForSymbol(tmp, 'BB:Y'));
    assert.equal(dbb.prepare('SELECT COUNT(*) n FROM bars').get().n, 7);
    dbb.close();
  });
  check('迁移：旧库没有被改也没有被删', () => {
    assert.ok(fs.existsSync(legacy));
    const l2 = new DatabaseSync(legacy, { readOnly: true });
    assert.equal(l2.prepare('SELECT COUNT(*) n FROM bars').get().n, 17);
    l2.close();
  });
  check('迁移：幂等，第二次跑直接跳过', () => {
    const again = migrateLegacy(tmp, { log: () => {} });
    assert.equal(again.migrated, false);
    assert.equal(again.reason, 'already-done');
  });
  check('迁移：索引登记了品种 -> 文件名的映射', () => {
    const idx = loadIndex(tmp);
    assert.ok(idx.symbols['AA:X']);
    assert.equal(symbolForSlug(tmp, idx.symbols['AA:X'].slug), 'AA:X');
  });
  fs.rmSync(tmp, { recursive: true, force: true });
}

fs.rmSync(root, { recursive: true, force: true });

console.log(`\n${passed} 项通过${failed ? `，${failed} 项失败` : '，全部通过'}`);
