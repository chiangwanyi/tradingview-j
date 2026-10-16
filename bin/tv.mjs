#!/usr/bin/env node
// tv —— TradingView 图表 WebSocket 抓取与入库
//
//   tv doctor                       诊断 CDP 能不能接上
//   tv targets                      列出可抓的标签页
//   tv capture [选项]                抓帧，落盘 data/raw/*.ndjson
//   tv ingest [选项]                 解码入库 data/bars.sqlite
//   tv stats                        看已入库的品种/区间/根数
//   tv export --symbol ... --out a.csv
//   tv send --frame '{"m":"..."}'   用页面里的 WebSocket 主动发帧
//   tv eval --expr 'document.title' 在目标页里求值

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  CdpBrowser,
  discoverEndpoint,
  devToolsPortFiles,
  openPage,
  pageTargets,
  readDevToolsActivePort,
  tcpProbe,
} from '../src/cdp.mjs';
import { capture } from '../src/capture.mjs';
import { ingest, createIngestor, formatSummary, listRawFiles } from '../src/ingest.mjs';
import { computeCoverage, renderCoverage, coverageLine, fmtTs } from '../src/coverage.mjs';
import { openStore, csvEscape, readBars, listSeriesIds, makeWriter } from '../src/store.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DB_FILE = path.join(ROOT, 'data', 'bars.sqlite');
const LEDGER_FILE = path.join(ROOT, 'data', 'ingest-state.json');

function readLedger() {
  try {
    return JSON.parse(fs.readFileSync(LEDGER_FILE, 'utf8'));
  } catch {
    return {};
  }
}

function writeLedger(o) {
  fs.mkdirSync(path.dirname(LEDGER_FILE), { recursive: true });
  fs.writeFileSync(LEDGER_FILE, JSON.stringify(o, null, 2) + '\n');
}

function parseArgs(argv) {
  const opts = { _: [], files: [] };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--file' || a === '-f') opts.files.push(argv[++i]);
    else if (a.startsWith('--file=')) opts.files.push(a.slice(7));
    else if (a.startsWith('--')) {
      const key = a.slice(2);
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--')) opts[key] = true;
      else {
        opts[key] = next;
        i++;
      }
    } else opts._.push(a);
  }
  return opts;
}

const num = (v, d) => (v === undefined || v === true ? d : Number(v));
const say = (m = '') => process.stdout.write(m + '\n');

async function cmdDoctor() {
  say('== TradingView 抓取环境诊断 ==');
  say(`Node ${process.version} · 原生 WebSocket: ${typeof WebSocket === 'function' ? '可用' : '缺失'}`);

  say('');
  say('Chrome 调试开关文件（DevToolsActivePort）：');
  for (const { id, file } of devToolsPortFiles()) {
    const ok = fs.existsSync(file);
    say(`  ${ok ? '[有]' : '[无]'} ${id.padEnd(12)} ${file}`);
    if (ok) {
      const lines = fs.readFileSync(file, 'utf8').trim().split(/\r?\n/).filter(Boolean);
      say(`         port=${lines[0]}  path=${lines[1] || '(无)'}`);
    }
  }

  say('');
  say('常见调试端口探测：');
  for (const p of [9222, 9229, 9333]) {
    say(`  ${(await tcpProbe(p)) ? '[通]' : '[堵]'} 127.0.0.1:${p}`);
  }

  say('');
  try {
    const ep = await discoverEndpoint();
    say(`端点：${ep.browserWsUrl}`);
    say(
      `HTTP 发现接口（/json/version）：${ep.httpDiscovery ? '可用' : '不可用（404）'}` +
        (ep.httpDiscovery ? '' : ' —— 新版 Chrome 只开放浏览器级 WS，属预期行为'),
    );

    const browser = await CdpBrowser.launch({ endpoint: ep });
    const targets = await browser.targets();
    const pages = pageTargets(targets);
    say(`页面标签：${pages.length} 个（经 ${ep.httpDiscovery ? 'HTTP 发现' : 'Target.getTargets'}）`);

    const tv = pages.filter((p) => /tradingview\.com/.test(p.url || ''));
    if (tv.length) {
      say('');
      say('找到 TradingView 页面：');
      for (const p of tv) say(`  - ${p.title}\n    ${p.url}`);
    } else {
      say('');
      say('没找到 TradingView 页面 —— 图表页需要和调试开关在同一个 Chrome 实例里打开。');
    }

    // 试着真 attach 一次，确认会话能建立
    const page = tv[0] || pages[0];
    if (page) {
      const session = await browser.attach(page);
      const title = await session.eval('document.title');
      say('');
      say(`attach 成功，页面标题读回：${JSON.stringify(title)}`);
      await browser.detach(session);
    }
    browser.close();
    say('');
    say('结果：可以接入。下一步 node bin/tv.mjs capture --reload --hook-ws --sample 40 --duration 120');
    return 0;
  } catch (e) {
    say(`结果：暂时接不上 —— ${e.message}`);
    if (e.tried?.length) for (const t of e.tried) say(`  port ${t.port}: ${t.why}`);
    say('');
    say('排查顺序：');
    say('  1. 在你正用着的那个 Chrome 里打开 chrome://inspect/#remote-debugging');
    say('  2. 勾选 "Allow remote debugging for this browser instance"');
    say('  3. 外部发起连接时 Chrome 会弹 "Allow remote debugging?" —— 必须点 Allow');
    say('  4. 重跑 tv doctor');
    say('');
    say('注意：Chrome >= 136 起，给默认用户数据目录加 --remote-debugging-port 会被静默忽略，');
    say('      「重启 Chrome 加参数」这条路走不通；每个新的浏览器会话都要重新勾一次开关。');
    return 1;
  }
}

async function cmdTargets(opts) {
  const ep = await discoverEndpoint();
  const browser = await CdpBrowser.launch({ endpoint: ep });
  const targets = await browser.targets();
  const pages = pageTargets(targets);
  say(`port ${ep.port} · ${pages.length} 个页面标签`);
  for (const p of pages) {
    const mark = /tradingview\.com/.test(p.url || '') ? '*' : ' ';
    say(`${mark} ${p.id || p.targetId}\n   ${p.title}\n   ${p.url}`);
  }
  browser.close();

  const picked = pages.find((p) => /tradingview\.com\/chart/.test(p.url || '')) || null;
  say('');
  say(picked ? `默认会抓这个：\n   ${picked.title}\n   ${picked.url}` : '没有匹配 tradingview.com/chart 的页面');
  return 0;
}

async function cmdCapture(opts) {
  const r = await capture({
    root: ROOT,
    match: opts.match ? String(opts.match).split(',') : ['tradingview.com/chart'],
    durationMs: num(opts.duration, 0) * 1000,
    idleStopMs: num(opts.idle, 0) * 1000,
    reload: Boolean(opts.reload),
    hookWs: Boolean(opts['hook-ws']),
    sampleCount: num(opts.sample, 0),
  });
  process.stdout.write(JSON.stringify({ file: r.file, sampleFile: r.sampleFile, recv: r.recv, sent: r.sent }, null, 2) + '\n');
  return 0;
}

function cmdIngest(opts) {
  const dbFile = opts.db ? String(opts.db) : DB_FILE;
  const symbol = opts.symbol && opts.symbol !== true ? String(opts.symbol) : null;
  const force = Boolean(opts.all || opts.force);

  const all = listRawFiles(ROOT).filter((f) => f.size > 0);
  if (!all.length && !opts.files.length) throw new Error('data/raw 下没有 .ndjson，先跑 tv capture');

  const ledger = readLedger();
  let files;

  if (opts.files.length) {
    files = opts.files;
  } else {
    // 增量判断：文件大小或修改时间变过就重扫（capture 还在追加时也安全，幂等）
    const changed = all.filter((f) => {
      const prev = ledger[f.name];
      return force || !prev || prev.size !== f.size || prev.mtimeMs !== f.mtimeMs;
    });
    files = changed.map((f) => f.file);
  }

  if (!files.length) {
    say('没有新文件需要入库 —— 台账里都处理过了。');
    say('（想强制全量重扫：tv ingest --all；台账文件：data/ingest-state.json）');
    return 0;
  }

  say(`待入库 ${files.length} 个文件${force ? '（--all 强制全量）' : '（增量）'}`);
  return ingest({ files, dbFile, symbol }).then((r) => {
    formatSummary(r.summary);
    for (const f of r.files) {
      ledger[f.name] = { size: f.size, mtimeMs: f.mtimeMs, lines: f.lines, bars: f.bars, at: Date.now() };
    }
    writeLedger(ledger);
    const db = openStore(dbFile);
    const t = db.prepare('SELECT COUNT(*) AS n, MIN(t) AS a, MAX(t) AS b FROM bars').get();
    say('');
    say(`库内合计 ${t.n} 根 · ${fmtTs(t.a)} → ${fmtTs(t.b)}`);
    db.close();
    return 0;
  });
}

async function cmdCoverage(opts) {
  const db = openStore(opts.db ? String(opts.db) : DB_FILE);
  const symbol = opts.symbol && opts.symbol !== true ? String(opts.symbol) : null;
  const reports = computeCoverage(db, { symbol });
  db.close();
  if (!reports.length) {
    say('库里还没有数据。先跑 tv watch（或 tv capture + tv ingest）。');
    return 0;
  }
  const withData = reports.filter((r) => r.bars > 0).sort((a, b) => (b.missingTotal || 0) - (a.missingTotal || 0));
  for (let i = 0; i < withData.length; i++) {
    if (i) say('');
    say(renderCoverage(withData[i], { maxDays: num(opts.top, 15) }));
  }
  return 0;
}

async function cmdWatch(opts) {
  const dbFile = opts.db ? String(opts.db) : DB_FILE;
  const symbol = opts.symbol && opts.symbol !== true ? String(opts.symbol) : null;
  const everyMs = num(opts.progress, 10) * 1000;

  const ing = createIngestor({ dbFile, symbol });

  say('== 常驻模式：抓到即入库 ==');
  say('在这个窗口里保持开着，去 TradingView 图表上用「回放」来回跳日期即可。');
  say('每抓到一个 WebSocket 帧就立刻解码入库，Ctrl+C 收尾。');
  say('');

  const stopAfterMs = num(opts.duration, 0) * 1000;
  let notified = false;

  const r = await capture({
    root: ROOT,
    match: opts.match ? String(opts.match).split(',') : ['tradingview.com/chart'],
    durationMs: stopAfterMs,
    idleStopMs: num(opts.idle, 0) * 1000,
    reload: Boolean(opts.reload),
    hookWs: Boolean(opts['hook-ws']),
    sampleCount: 0,
    onRecord: (rec) => ing.push(rec),
    progressEveryMs: everyMs,
    onProgress: ({ recv, sent, elapsedMs }) => {
      ing.flush();
      const reports = computeCoverage(ing.db, { symbol });
      const head = `[${new Date().toTimeString().slice(0, 8)}] +${Math.round(elapsedMs / 1000)}s · ` +
        `帧 收${recv}/发${sent} · 本次入库 ${ing.summary.bars} 根`;
      say(head);
      if (reports.length) {
        for (const rep of reports) say(`           ${coverageLine(rep)}`);
      } else {
        say('           还没有解出 K 线 —— 如果刚开了回放，跳到别的日期让它重新拉数据。');
      }
      notified = true;
    },
  });

  ing.flush();
  const reports = computeCoverage(ing.db, { symbol });
  ing.close();

  say('');
  say(`抓帧结束：收到 ${r.recv} 帧 / 发出 ${r.sent} 帧 · 原始记录 ${r.file}`);
  say(`本次解码写入 ${ing.summary.bars} 根 K 线（含幂等覆盖）`);
  for (const rep of reports) say(renderCoverage(rep, { maxDays: num(opts.top, 10) }));
  if (!notified) say('（期间没有收到帧 —— 检查目标页是否还在，或回放时页面是否真的在请求数据）');
  return 0;
}

async function cmdStats(opts) {
  const db = openStore(opts.db || DB_FILE);
  const total = db.prepare('SELECT COUNT(*) AS n, COUNT(DISTINCT t) AS d FROM bars').get();
  const ids = listSeriesIds(db);
  say(`总 K 线行数：${total.n} · 不同时间戳：${total.d}`);
  if (!ids.length) {
    say('还没有数据。');
    db.close();
    return 0;
  }
  say('');
  const fmt = (t) => (t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 19) : '-');
  for (const s of ids) {
    say(`${s.symbol} · series ${s.series}`);
    say(`   ${s.bars} 行 / ${s.distinct_t} 个不同时间戳  ${fmt(s.first_t)} → ${fmt(s.last_t)}`);
  }
  if (ids.length > 1) {
    say('');
    say('注意：series（sds_1 / sds_2 …）是每条 WebSocket 连接自己发的临时订阅号，');
    say('      页面重连后会换号，所以同一个品种可能有多行 —— 内容一致，导出时会自动收敛。');
  }
  say('');
  const w = makeWriter(db);
  const meta = w.listSeries();
  for (const m of meta) say(`周期 ${m.interval || '未知'} · ${m.symbol}/${m.series} · ${m.bars} 根`);
  say('');
  say('提示：区间只是「最早↔最晚」，中间可能有空洞。看空洞请跑 tv coverage。');
  db.close();
  return 0;
}

async function cmdExport(opts) {
  const db = openStore(opts.db || DB_FILE);
  const symbol = opts.symbol && opts.symbol !== true ? String(opts.symbol) : null;
  const interval = opts.interval && opts.interval !== true ? String(opts.interval) : null;
  const dedupe = !opts.raw;
  const { rows, total, collapsed } = readBars(db, {
    symbol,
    interval,
    dedupe,
    limit: opts.limit === undefined ? 0 : Number(opts.limit),
  });
  db.close();

  const out = opts.out ? path.resolve(String(opts.out)) : path.join(ROOT, 'data', 'bars.csv');
  fs.mkdirSync(path.dirname(out), { recursive: true });
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
  fs.writeFileSync(out, lines.join('\n') + '\n');
  say(`导出 ${rows.length} 行 → ${out}`);
  if (dedupe && collapsed) {
    say(`（库内 ${total} 行，按 symbol+interval+t 收敛掉 ${collapsed} 行 —— 来自 series id 重连换号，用 --raw 可保留）`);
  }
  return 0;
}

async function withTarget(opts, fn) {
  const { browser, session, target } = await openPage({ match: opts.match || 'tradingview.com/chart' });
  try {
    return await fn(session, target);
  } finally {
    await browser.detach(session);
    browser.close();
  }
}

async function cmdSend(opts) {
  let payload = opts.frame;
  if (typeof payload !== 'string') throw new Error('用法：tv send --frame \'{"m":"...","p":[...]}\'');
  if (payload.startsWith('@')) payload = fs.readFileSync(payload.slice(1), 'utf8').trim();
  let parsed;
  try {
    parsed = JSON.parse(payload);
  } catch {
    parsed = payload;
  }
  return withTarget(opts, async (session) => {
    const needle = opts.socket || 'prodata';
    const result = await session.eval(
      `window.__tvSend(${JSON.stringify(parsed)}, ${JSON.stringify(needle)})`,
      { returnByValue: true },
    );
    say(JSON.stringify(result, null, 2));
    if (!result?.ok) {
      say('提示：需要在 capture 时加 --hook-ws 并刷新过页面，页面里才有 __tvSend。');
      return 1;
    }
    return 0;
  });
}

async function cmdEval(opts) {
  const expr = typeof opts.expr === 'string' ? opts.expr : 'document.title';
  return withTarget(opts, async (session) => {
    const v = await session.eval(expr, { returnByValue: true, awaitPromise: true });
    say(typeof v === 'string' ? v : JSON.stringify(v, null, 2));
    return 0;
  });
}

// 拖动图表：把可视窗口往回拖 -> 图表需要更早的 K 线 -> 会去 WebSocket 要数据。
// 这是"自动填时间轴"的最小操作原语。
// dx > 0 = 拖向右侧 = 看更早的数据；dx < 0 = 看更晚的数据。
async function dragPan(session, { cx, cy, dx, dy = 0, segments = 6 }) {
  await session.send('Input.dispatchMouseEvent', {
    type: 'mousePressed', x: cx, y: cy, button: 'left', buttons: 1, clickCount: 1,
  });
  // 分几步移动，图表才认得出是拖拽而不是瞬移
  for (let s = 1; s <= segments; s++) {
    await session.send('Input.dispatchMouseEvent', {
      type: 'mouseMoved',
      x: Math.round(cx + (dx * s) / segments),
      y: cy + dy,
      button: 'left',
      buttons: 1,
    });
    await sleep(30);
  }
  await session.send('Input.dispatchMouseEvent', {
    type: 'mouseReleased',
    x: Math.round(cx + dx),
    y: cy + dy,
    button: 'left',
    buttons: 0,
    clickCount: 1,
  });
}

async function cmdPan(opts) {
  const dx = num(opts.dx, 700);
  const dy = num(opts.dy, 0);
  const times = num(opts.times, 1);
  const pause = num(opts.pause, 700);
  return withTarget(opts, async (session) => {
    const geo = JSON.parse(
      await session.eval('JSON.stringify({w: window.innerWidth, h: window.innerHeight})', { returnByValue: true }),
    );
    const cx = Math.round(geo.w * 0.5);
    const cy = Math.round(geo.h * 0.45);
    say(`在 (${cx}, ${cy}) 拖动 ×${times}，每次 dx=${dx}（窗口 ${geo.w}×${geo.h}）`);
    for (let i = 0; i < times; i++) {
      await dragPan(session, { cx, cy, dx, dy });
      say(`  第 ${i + 1} 次拖动完成`);
      await sleep(pause);
    }
    return 0;
  });
}

// 自动回填：反复把图表往回拖，每拖一次等数据静止，直到连续若干次没有新 K 线。
// 这是"尽可能下载完整时间轴"的自动化版本 —— 不需要手动回放。
async function cmdBackfill(opts) {
  const dbFile = opts.db ? String(opts.db) : DB_FILE;
  const symbol = opts.symbol && opts.symbol !== true ? String(opts.symbol) : null;
  const dx = num(opts.dx, 1200);
  const maxSteps = num(opts['max-steps'], 40);
  const quietMs = num(opts.quiet, 3) * 1000;
  const stopAfter = num(opts['stop-after'], 4);
  const betweenMs = num(opts.between, 800);
  const hardCapMs = num(opts['max-minutes'], 40) * 60000;
  const restore = !opts['no-restore'];

  const ing = createIngestor({ dbFile, symbol });
  const snap = () => {
    const sql =
      'SELECT COUNT(*) n, MIN(t) a, MAX(t) b FROM bars' + (symbol ? ' WHERE symbol = ?' : '');
    return symbol ? ing.db.prepare(sql).get(symbol) : ing.db.prepare(sql).get();
  };
  const fmt = (t) => (t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 16) : '-');

  const s0 = snap();
  say('== 自动回填：反复往回拖图表，把历史一段段拉回来 ==');
  say(`当前库内 ${s0.n} 根，最早 ${fmt(s0.a)}`);
  say(`参数：每次拖 ${dx}px · 最多 ${maxSteps} 步 · 连续 ${stopAfter} 步无新增即停 · 硬上限 ${hardCapMs / 60000} 分钟`);
  say('');
  say('注意：这会连续操作你的图表页面并把窗口定格在历史位置，结束后可以自己拖回来。');
  say('');

  let stopFlag = false;
  let lastFrameAt = Date.now();
  let driver = null;
  const startedAt = Date.now();
  // 图表真发出去的历史请求次数。这是判断"拖拽有没有生效"的唯一硬信号：
  // 没发 request_more_data 说明可视范围还没走出已缓存的那一窗，
  // 跟"已经拉到边界"是两回事，不该混为一谈。
  let moreDataRequests = 0;

  const capPromise = capture({
    root: ROOT,
    match: opts.match ? String(opts.match).split(',') : ['tradingview.com/chart'],
    reload: false,
    onRecord: (rec) => {
      lastFrameAt = Date.now();
      if (
        rec.k === 'frame' &&
        rec.dir === 'send' &&
        typeof rec.text === 'string' &&
        rec.text.includes('request_more_data')
      ) {
        moreDataRequests++;
      }
      ing.push(rec);
    },
    shouldStop: () => stopFlag || Date.now() - startedAt > hardCapMs,
    onReady: ({ session }) => {
      driver = (async () => {
        const geo = JSON.parse(
          await session.eval('JSON.stringify({w: window.innerWidth, h: window.innerHeight})', {
            returnByValue: true,
          }),
        );
        const cx = Math.round(geo.w * 0.5);
        const cy = Math.round(geo.h * 0.45);

        // 后台标签页的渲染循环会被节流，图表可能压根不处理拖拽 ——
        // 我们踩过这个：同一段拖动代码在 `tv pan` 里有效、在常驻会话里无效。
        // 提到前台 + 预热一下，比事后猜"为什么拖了没反应"省事。
        await session.send('Page.bringToFront').catch(() => {});
        await sleep(1200);

        let noGain = 0;
        let moved = 0;
        for (let step = 1; step <= maxSteps; step++) {
          const before = snap();
          const moreBefore = moreDataRequests;
          const t0 = Date.now();
          lastFrameAt = Date.now();

          await session.send('Page.bringToFront').catch(() => {});
          await dragPan(session, { cx, cy, dx });
          moved++;

          // 等这一轮的数据到齐：静默 quietMs 就认为到齐了，但最多等 quietMs+8s
          while (Date.now() - lastFrameAt < quietMs && Date.now() - t0 < quietMs + 8000) {
            await sleep(200);
          }
          ing.flush();

          const after = snap();
          const gained = after.n - before.n;
          const asked = moreDataRequests - moreBefore;

          say(
            `  ${String(step).padStart(2)}/${maxSteps}  新增 ${String(gained).padStart(4)} 根 · ` +
              `共 ${after.n} 根 · 最早 ${fmt(after.a)} · 图表请求 ${asked} 次`,
          );
          // 区分两种"没新增"：图表没请求（可视范围还没走出缓存窗口）vs 请求了但没有更早的数据（真到边界）
          if (gained === 0 && asked === 0) {
            say('        图表这一步没发 request_more_data —— 可视范围还没走出已缓存的那一窗，继续拖会遇到边界');
          }

          if (gained === 0) {
            noGain++;
            if (noGain >= stopAfter) {
              say(`  连续 ${noGain} 步没有新数据 —— 已到可拉取的边界（或页面不再请求了），收工。`);
              break;
            }
          } else {
            noGain = 0;
          }
          await sleep(betweenMs);
        }

        // 回填会把视图留在很久以前，默认拨回现在
        if (restore && moved) {
          say(`  恢复视图：反向拖 ${moved} 次…`);
          await session.send('Page.bringToFront').catch(() => {});
          for (let i = 0; i < moved; i++) {
            await dragPan(session, { cx, cy, dx: -dx });
            await sleep(250);
          }
        }
        stopFlag = true;
      })().catch((e) => {
        say(`[驱动器异常] ${e.message}`);
        stopFlag = true;
      });
    },
  });

  const r = await capPromise;
  if (driver) await driver.catch(() => {});
  ing.flush();

  const s1 = snap();
  const reports = computeCoverage(ing.db, { symbol });
  ing.close();

  say('');
  say(`回填结束：本次共 ${s1.n - s0.n} 根新 K 线（库内合计 ${s1.n} 根）`);
  say(`范围 ${fmt(s1.a)} → ${fmt(s1.b)}  ·  原始记录 ${r.file}`);
  for (const rep of reports) {
    say('');
    say(renderCoverage(rep, { maxDays: num(opts.top, 10) }));
  }
  return 0;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 截图：验证图表实际停在哪个位置（拖动/回放是否真的生效，靠肉眼比猜可靠）
async function cmdShot(opts) {
  const out = opts.out ? path.resolve(String(opts.out)) : path.join(ROOT, 'data', 'shot.png');
  return withTarget(opts, async (session) => {
    await session.send('Page.enable');
    const r = await session.send('Page.captureScreenshot', { format: 'png' });
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.writeFileSync(out, Buffer.from(r.data, 'base64'));
    say(`已保存截图 → ${out}`);
    return 0;
  });
}

// 通用 CDP 透传：调试用，也方便试新协议时不用改代码
async function cmdCdp(opts) {
  const method = typeof opts.method === 'string' ? opts.method : null;
  if (!method) throw new Error(`用法：tv cdp --method Input.dispatchMouseEvent --params '{"...":1}'`);
  const params = opts.params && opts.params !== true ? JSON.parse(String(opts.params)) : {};
  return withTarget(opts, async (session) => {
    const r = await session.send(method, params);
    say(JSON.stringify(r ?? null, null, 2));
    return 0;
  });
}

const COMMANDS = {
  doctor: cmdDoctor,
  targets: cmdTargets,
  watch: cmdWatch,
  capture: cmdCapture,
  ingest: cmdIngest,
  coverage: cmdCoverage,
  stats: cmdStats,
  export: cmdExport,
  send: cmdSend,
  eval: cmdEval,
  cdp: cmdCdp,
  pan: cmdPan,
  backfill: cmdBackfill,
  shot: cmdShot,
};

const USAGE = `tv —— TradingView WebSocket 抓取与入库

  tv doctor                              诊断 CDP 接入
  tv targets [--match <子串>]             列出可抓标签页

  # 回放抓数据的日常用法（推荐）
  tv watch [--symbol CME_MINI:MNQ1!] [--progress 10]
          常驻：抓到帧即解码入库，定期打印覆盖度。Ctrl+C 收尾。

  # 全自动回填历史（不必手动回放：程序自己把图表往回拖）
  tv backfill [--max-steps 14] [--dx 1200] [--stop-after 4] [--no-restore] [--symbol X]
          每拖一次等数据到齐，连续若干步无新增即停，结束自动拨回视图

  # 分步用法
  tv capture [--duration 秒] [--idle 秒] [--reload] [--hook-ws] [--sample N]
  tv ingest [--file a.ndjson ...] [--all] [--symbol X]
          默认只处理新增/变动过的抓包文件（台账 data/ingest-state.json）

  tv coverage [--symbol X] [--top 15]    覆盖度报告：哪里还是空的
  tv stats
  tv export [--symbol X] [--interval 5m] [--limit N] [--raw] [--out data/bars.csv]
          默认按 symbol+interval+t 收敛重复行（series id 重连会换号）
  tv send --frame '<JSON>' [--socket prodata]
  tv eval --expr '<JS>'
  tv pan [--dx 700] [--times 3] [--pause 700]      拖动图表往回看（触发历史数据请求）
  tv shot [--out data/shot.png]                    截图，确认图表停在哪
  tv cdp --method <CDP方法> [--params '<JSON>']     通用 CDP 透传
`;

// 正常路径下所有流的 end() 都已经 await 过，这里只需要确保 stdout 排空后退出。
// 加这一道是因为 CDP 的 WebSocket 在某些情况下会拖住事件循环（我们踩过：
// 漏掉 close() 导致进程永不退出，工具侧只能 SIGTERM，未落盘的 stdout 全丢）。
function exitWhenFlushed(code) {
  const finish = () => process.exit(code);
  try {
    if (process.stdout.writableLength === 0 && process.stderr.writableLength === 0) return finish();
    let pending = 2;
    const one = () => {
      if (--pending <= 0) finish();
    };
    process.stdout.write('', one);
    process.stderr.write('', one);
  } catch {
    finish();
  }
}

async function main() {
  const [, , cmd, ...rest] = process.argv;
  if (!cmd || cmd === '-h' || cmd === '--help' || cmd === 'help') {
    process.stdout.write(USAGE);
    return 0;
  }
  const fn = COMMANDS[cmd];
  if (!fn) {
    process.stderr.write(`未知命令：${cmd}\n\n${USAGE}`);
    return 2;
  }
  const opts = parseArgs(rest);
  return (await fn(opts)) ?? 0;
}

main()
  .then((code) => exitWhenFlushed(code || 0))
  .catch((e) => {
    process.stderr.write(`\n[错误] ${e.message}\n`);
    if (process.env.TV_TRACE) process.stderr.write(e.stack + '\n');
    exitWhenFlushed(1);
  });
