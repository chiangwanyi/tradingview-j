#!/usr/bin/env node
// tv —— 命令行入口
//
// 现在日常用法是 `npm start` 开网页，这个 CLI 保留下来是为了：
//   - 无头/脚本化场景（cron、批处理）
//   - 排查问题（doctor / targets / eval / cdp / shot）
//   - 一次性导出与迁移
//
// 页面里能做的事（启停抓取、切自动回拉、看覆盖日历）在这里都有对应命令，
// 两边共用 src/session.mjs 这一个会话控制器，不存在"网页修了 CLI 没修"的分叉。

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  CdpBrowser,
  discoverEndpoint,
  devToolsPortFiles,
  openPage,
  pageTargets,
  tcpProbe,
} from '../src/cdp.mjs';
import { capture } from '../src/capture.mjs';
import { CaptureSession, DEFAULT_SETTINGS, dragPan } from '../src/session.mjs';
import { pickChangedFiles, readLedger, runIngest, writeLedger, formatSummary } from '../src/ingest.mjs';
import { listRawFiles, rawIndexFile } from '../src/rawindex.mjs';
import { computeCoverage, renderCoverage } from '../src/coverage.mjs';
import { buildCalendar } from '../src/calendar.mjs';
import { migrateLegacy, legacyStatus } from '../src/migrate.mjs';
import { openStore, csvEscape, readBars, listSeriesIds, summarize } from '../src/store.mjs';
import { listDbFiles, fileForSymbol, symbolForSlug, dbDir, legacyDbFile } from '../src/registry.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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

// ---------------------------------------------------------------- 品种定位

// 支持 --symbol CME_MINI:MNQ1!、--symbol CME_MINI_MNQ1_（slug）、--slug xxx
function resolveTargetSymbol(opts) {
  if (opts.slug && opts.slug !== true) {
    const sym = symbolForSlug(ROOT, String(opts.slug));
    if (!sym) throw new Error(`索引里没有 slug 为 ${opts.slug} 的品种`);
    return sym;
  }
  if (opts.symbol && opts.symbol !== true) {
    const s = String(opts.symbol);
    const bySlug = symbolForSlug(ROOT, s);
    return bySlug || s;
  }
  return null;
}

function listSymbols() {
  return listDbFiles(ROOT)
    .filter((f) => !f.missing)
    .map((f) => {
      let summary = { bars: 0, rows: 0, series: [] };
      let db = null;
      try {
        db = openStore(f.file);
        summary = summarize(db);
      } catch (e) {
        summary = { error: e.message, bars: 0, rows: 0, series: [] };
      } finally {
        try {
          db?.close();
        } catch {}
      }
      return { ...f, ...summary };
    })
    .sort((a, b) => (b.bars || 0) - (a.bars || 0));
}

// ---------------------------------------------------------------- 命令

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
    say('结果：可以接入。下一步 npm start 打开网页，点「开始抓取」。');
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

async function cmdTargets() {
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

function cmdSymbols() {
  const all = listSymbols();
  const dir = dbDir(ROOT);
  say(`品种库目录：${dir}`);
  const legacy = legacyStatus(ROOT);
  if (legacy.exists) {
    say(
      `旧版混合库 ${legacyDbFile(ROOT)} 仍存在` +
        (legacy.unchanged ? '（已迁移过）' : '（尚未迁移，跑 tv migrate）'),
    );
  }
  say('');
  if (!all.length) {
    say('还没有品种库。');
    return 0;
  }
  const fmt = (t) => (t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 16) : '-');
  for (const s of all) {
    say(`${s.symbol || s.slug}  [${s.filename}]`);
    say(
      `   ${s.bars} 根 / ${s.rows} 行 · 周期 ${s.interval || '未知'} · ` +
        `${fmt(s.firstT)} → ${fmt(s.lastT)}`,
    );
    if (s.seriesCount > 1) {
      say(`   注意：出现过 ${s.seriesCount} 个 series（${s.series.map((x) => x.series).join(', ')}）—— 重连换号，行数会多于根数`);
    }
  }
  return 0;
}

function cmdMigrate(opts) {
  const r = migrateLegacy(ROOT, { force: Boolean(opts.force), log: say });
  if (!r.migrated) {
    const why = {
      'no-legacy': '没有旧版混合库，无需迁移。',
      'already-done': '已经迁移过了（旧文件没再变过）。要强制重跑加 --force。',
      'legacy-empty': '旧库里没有数据。',
      'legacy-has-no-bars-table': '旧库里没有 bars 表，跳过。',
    }[r.reason] || r.reason;
    say(why);
  }
  return 0;
}

async function cmdServe(opts) {
  const port = num(opts.port, 8787);
  const { spawn } = await import('node:child_process');
  const child = spawn(process.execPath, [path.join(ROOT, 'server', 'index.mjs'), '--port', String(port)], {
    stdio: 'inherit',
    cwd: ROOT,
  });
  return new Promise((resolve) => {
    child.on('exit', (code) => resolve(code || 0));
  });
}

// 常驻会话（网页上的「开始抓取」等价命令）
async function runSessionCommand(opts, overrides = {}) {
  const session = new CaptureSession({
    root: ROOT,
    settings: {
      ...DEFAULT_SETTINGS,
      symbol: resolveTargetSymbol(opts) || '',
      match: opts.match ? String(opts.match) : DEFAULT_SETTINGS.match,
      autoPull: Boolean(opts.autopull) || Boolean(overrides.autoPull),
      dx: num(opts.dx, DEFAULT_SETTINGS.dx),
      maxSteps: num(opts['max-steps'], DEFAULT_SETTINGS.maxSteps),
      stopAfter: num(opts['stop-after'], DEFAULT_SETTINGS.stopAfter),
      betweenSec: num(opts.between, DEFAULT_SETTINGS.betweenSec),
      quietSec: num(opts.quiet, DEFAULT_SETTINGS.quietSec),
      maxMinutes: num(opts['max-minutes'], DEFAULT_SETTINGS.maxMinutes),
      restoreView: !opts['no-restore'],
      hookWs: Boolean(opts['hook-ws']),
      reloadOnStart: Boolean(opts.reload),
    },
  });
  session.on('log', (l) => say(l.text));
  session.on('step', (s) => say(`        ↳ 累计 ${s.total} 根`));

  const durationMs = num(opts.duration, 0) * 1000;
  const timer = durationMs ? setTimeout(() => session.stop('duration'), durationMs) : null;

  const onSignal = () => session.stop('signal');
  process.once('SIGINT', onSignal);
  process.once('SIGTERM', onSignal);

  say('');
  await session.start().catch((e) => say(`启动失败：${e.message}`));
  const snap = await session.wait();
  if (timer) clearTimeout(timer);
  process.removeListener('SIGINT', onSignal);
  process.removeListener('SIGTERM', onSignal);

  say('');
  say(`会话结束：收 ${snap.counters.recv} / 发 ${snap.counters.sent} 帧 · 本会话新增 ${snap.addedBars} 根`);
  const sym = snap.symbol;
  if (sym) {
    const db = openStore(fileForSymbol(ROOT, sym));
    try {
      const rep = computeCoverage(db)[0];
      if (rep) {
        say('');
        say(renderCoverage(rep, { maxDays: num(opts.top, 10) }));
      }
    } finally {
      db.close();
    }
  }
  return 0;
}

async function cmdWatch(opts) {
  say('== 常驻模式 ==');
  say('默认只被动监听：去那个 Chrome 里用回放/拖动挑日期，抓到就入库。');
  if (opts.autopull) say('（--autopull 已开启：程序会主动拖动图表来拉历史）');
  return runSessionCommand(opts);
}

function cmdCapture(opts) {
  return capture({
    root: ROOT,
    match: opts.match ? String(opts.match).split(',') : ['tradingview.com/chart'],
    durationMs: num(opts.duration, 0) * 1000,
    idleStopMs: num(opts.idle, 0) * 1000,
    reload: Boolean(opts.reload),
    hookWs: Boolean(opts['hook-ws']),
    sampleCount: num(opts.sample, 0),
    symbolHint: resolveTargetSymbol(opts),
  }).then((r) => {
    say(JSON.stringify({ file: r.file, recv: r.recv, sent: r.sent }, null, 2));
    return 0;
  });
}

function cmdIngest(opts) {
  const symbol = resolveTargetSymbol(opts);
  const force = Boolean(opts.all || opts.force);

  if (opts.files.length) {
    return runIngest({ root: ROOT, files: opts.files, symbol }).then((r) => {
      formatSummary(r.summary);
      const led = readLedger(ROOT);
      for (const f of r.files) {
        led[f.name] = { size: f.size, mtimeMs: f.mtimeMs, lines: f.lines, bars: f.bars, at: Date.now() };
      }
      writeLedger(ROOT, led);
      return 0;
    });
  }

  const { files, ledger, all } = pickChangedFiles(ROOT, { symbol, force });
  if (!files.length) {
    say(`没有新文件需要入库（共 ${all.length} 个，都已处理过）。`);
    say('（想强制全量重扫：tv ingest --all）');
    return 0;
  }
  say(`待入库 ${files.length} / ${all.length} 个文件${force ? '（--all 强制全量）' : '（增量）'}`);
  return runIngest({ root: ROOT, files, symbol }).then((r) => {
    formatSummary(r.summary);
    for (const f of r.files) {
      ledger[f.name] = { size: f.size, mtimeMs: f.mtimeMs, lines: f.lines, bars: f.bars, at: Date.now() };
    }
    writeLedger(ROOT, ledger);
    return 0;
  });
}

function cmdCoverage(opts) {
  const sym = resolveTargetSymbol(opts);
  const targets = sym ? [sym] : listSymbols().map((s) => s.symbol || s.slug);
  if (!targets.length) {
    say('还没有品种库。先 npm start 抓一次，或 tv ingest。');
    return 0;
  }
  const top = num(opts.top, 15);
  for (const s of targets) {
    let db = null;
    try {
      db = openStore(fileForSymbol(ROOT, s));
      const reports = computeCoverage(db);
      if (!reports.length) continue;
      for (const rep of reports) {
        say('');
        say(renderCoverage(rep, { maxDays: top }));
      }
    } catch (e) {
      say(`[${s}] 读取失败：${e.message}`);
    } finally {
      try {
        db?.close();
      } catch {}
    }
  }
  return 0;
}

function cmdCalendar(opts) {
  const sym = resolveTargetSymbol(opts);
  if (!sym) throw new Error('需要 --symbol 或 --slug 指定品种（tv symbols 可以看有哪些）');
  const db = openStore(fileForSymbol(ROOT, sym));
  try {
    const cal = buildCalendar(db, { windowDays: num(opts.window, 0) });
    if (cal.empty) {
      say('这个品种库里还没有数据。');
      return 0;
    }
    say(`${cal.symbol} · ${cal.interval} · 每格 ${cal.bucket}`);
    say(`范围 ${cal.range.fromIso} → ${cal.range.toIso}（${cal.range.days} 天）`);
    if (cal.summary) {
      say(
        `覆盖率 ${cal.summary.coverageRatio == null ? '?' : (cal.summary.coverageRatio * 100).toFixed(1) + '%'}` +
          ` · 缺 ${cal.summary.missingTotal} 根 · ${cal.summary.gapDays} 天有缺口 · 断成 ${cal.summary.breakCount + 1} 段`,
      );
      if (cal.summary.nextTarget) say(`建议下一个目标：${cal.summary.nextTarget.iso}`);
    }
    say('');
    // 文本版条带：一天一行，'█' 有数据、'·' 缺、' ' 非交易时段
    const glyph = { 1: '█', 2: '·', 3: '▒', 0: ' ' };
    const step = Math.max(1, Math.ceil(cal.slotsPerDay / 96));
    const shown = cal.days.slice(-num(opts.days, 60));
    for (const d of shown) {
      let row = '';
      for (let i = 0; i < cal.slotsPerDay; i += step) {
        let any = '0';
        for (let j = i; j < Math.min(i + step, cal.slotsPerDay); j++) {
          const ch = d.states[j];
          if (ch === '1' || ch === '3') { any = '1'; break; }
          if (ch === '2') any = '2';
        }
        row += glyph[any];
      }
      say(`${d.iso} ${String(d.n).padStart(5)}  ${row}`);
    }
    say('');
    say('图例：█ 有数据　· 应有但缺　▒ 有数据但无参照　(空格) 非交易时段');
    say(`（每天按 ${step} 格合并显示，完整数据在网页「覆盖视图」里）`);
  } finally {
    db.close();
  }
  return 0;
}

function cmdStats(opts) {
  const sym = resolveTargetSymbol(opts);
  if (sym) {
    const file = fileForSymbol(ROOT, sym);
    const db = openStore(file);
    try {
      const ids = listSeriesIds(db);
      const s = summarize(db);
      say(`${s.symbol} · 周期 ${s.interval || '未知'}`);
      say(`${s.bars} 根 / ${s.rows} 行 · ${fmtTs(s.firstT)} → ${fmtTs(s.lastT)}`);
      say(`库文件 ${file}`);
      say('');
      for (const x of ids) say(`  series ${x.series}: ${x.bars} 行 / ${x.distinct_t} 个时间戳`);
      if (ids.length > 1) {
        say('（series 是每条 WebSocket 连接自己发的临时订阅号，重连会换号，所以行数可能多于根数）');
      }
    } finally {
      db.close();
    }
    return 0;
  }
  return cmdSymbols();
}

const fmtTs = (t) => (t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 16) : '-');

function cmdExport(opts) {
  const sym = resolveTargetSymbol(opts);
  if (!sym) throw new Error('需要 --symbol 或 --slug 指定品种（tv symbols 可以看有哪些）');
  const db = openStore(fileForSymbol(ROOT, sym));
  try {
    const interval = opts.interval && opts.interval !== true ? String(opts.interval) : null;
    const dedupe = !opts.raw;
    const { rows, total, collapsed } = readBars(db, {
      interval,
      dedupe,
      limit: opts.limit === undefined ? 0 : Number(opts.limit),
    });
    const out = opts.out
      ? path.resolve(String(opts.out))
      : path.join(ROOT, 'data', `bars_${sym.replace(/[^\w.-]/g, '_')}.csv`);
    fs.mkdirSync(path.dirname(out), { recursive: true });
    const lines = ['symbol,interval,t,iso,o,h,l,c,v'];
    for (const r of rows) {
      lines.push(
        [csvEscape(r.symbol), csvEscape(r.interval), r.t, new Date(r.t * 1000).toISOString(), r.o, r.h, r.l, r.c, r.v].join(','),
      );
    }
    fs.writeFileSync(out, lines.join('\n') + '\n');
    say(`导出 ${rows.length} 行 → ${out}`);
    if (dedupe && collapsed) {
      say(`（库内 ${total} 行，按 symbol+interval+t 收敛掉 ${collapsed} 行 —— 来自 series 重连换号，用 --raw 可保留）`);
    }
  } finally {
    db.close();
  }
  return 0;
}

function cmdRaw(opts) {
  const sym = resolveTargetSymbol(opts);
  const files = listRawFiles(ROOT, { symbol: sym });
  const ledger = readLedger(ROOT);
  say(`索引文件：${rawIndexFile(ROOT)}`);
  say(`共 ${files.length} 个抓包文件`);
  say('');
  for (const f of files) {
    const led = ledger[f.name];
    const pending = !led || led.size !== f.size || led.mtimeMs !== f.mtimeMs;
    say(
      `${pending ? '[待入库]' : '[已入库]'} ${f.name}  ${(f.size / 1024).toFixed(0)}KB  ` +
        `${f.symbol || '(品种未知)'}  ${new Date(f.mtimeMs).toISOString().slice(0, 16)}`,
    );
  }
  return 0;
}

// ---------------------------------------------------------------- 页面操作

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
  if (typeof payload !== 'string') throw new Error(`用法：tv send --frame '{"m":"...","p":[...]}'`);
  if (payload.startsWith('@')) payload = fs.readFileSync(payload.slice(1), 'utf8').trim();
  let parsed;
  try {
    parsed = JSON.parse(payload);
  } catch {
    parsed = payload;
  }
  return withTarget(opts, async (session) => {
    const needle = opts.socket || 'prodata';
    const result = await session.eval(`window.__tvSend(${JSON.stringify(parsed)}, ${JSON.stringify(needle)})`, {
      returnByValue: true,
    });
    say(JSON.stringify(result, null, 2));
    return result?.ok ? 0 : 1;
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

async function cmdPan(opts) {
  const dx = num(opts.dx, 700);
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
      await dragPan(session, { cx, cy, dx });
      say(`  第 ${i + 1} 次拖动完成`);
      await sleep(pause);
    }
    return 0;
  });
}

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
  symbols: cmdSymbols,
  migrate: cmdMigrate,
  serve: cmdServe,
  watch: cmdWatch,
  backfill: (opts) => runSessionCommand(opts, { autoPull: true }),
  capture: cmdCapture,
  ingest: cmdIngest,
  raw: cmdRaw,
  coverage: cmdCoverage,
  calendar: cmdCalendar,
  stats: cmdStats,
  export: cmdExport,
  send: cmdSend,
  eval: cmdEval,
  pan: cmdPan,
  shot: cmdShot,
  cdp: cmdCdp,
};

const USAGE = `tv —— TradingView 抓取工具（网页版是 npm start）

  日常用法
    npm start                                 启动本地工作台（含网页界面）
    tv serve [--port 8787]                    同上，从 CLI 启动

  抓取
    tv watch [--symbol X] [--duration 秒] [--autopull] [--hook-ws]
              常驻：抓到帧即解码入库。默认只被动监听（你自己在图表上回放）
    tv backfill [--max-steps 40] [--dx 1200] [--stop-after 4] [--no-restore]
              等价于 watch --autopull：程序主动往回拖图表拉历史
    tv capture [--duration 秒] [--reload] [--hook-ws] [--sample N]
              只落盘原始帧，不解码（排查用）

  数据
    tv symbols                                列出所有品种库
    tv stats --symbol X                       看某个品种的根数/区间/series
    tv coverage [--symbol X] [--top 15]       覆盖度报告：哪里还是空的
    tv calendar --symbol X [--days 60]        文本版覆盖日历（网页版是图形化的）
    tv export --symbol X [--interval 5m] [--out a.csv]
    tv raw [--symbol X]                       抓包文件与入库台账
    tv ingest [--all] [--symbol X]            入库新增/变动的抓包文件

  存储
    tv migrate [--force]                      把旧的 data/bars.sqlite 拆成每品种一个文件

  诊断 / 调试
    tv doctor                                 诊断 CDP 接入
    tv targets                                列出可抓标签页
    tv eval --expr '<JS>'                     在目标页里求值
    tv pan [--dx 900] [--times 3]             拖动图表往回看
    tv shot [--out data/shot.png]             截图
    tv send --frame '<JSON>' [--socket prodata]
    tv cdp --method <CDP方法> [--params '<JSON>']
`;

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
