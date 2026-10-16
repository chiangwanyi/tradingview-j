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
import { ingest, latestRawFile } from '../src/ingest.mjs';
import { openStore, makeWriter, csvEscape } from '../src/store.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DB_FILE = path.join(ROOT, 'data', 'bars.sqlite');

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

async function cmdIngest(opts) {
  const files = opts.files.length ? opts.files : [latestRawFile(ROOT)];
  if (!files[0]) throw new Error('data/raw 下没有 .ndjson，先跑 tv capture');
  await ingest({ files, dbFile: opts.db || DB_FILE });
  return 0;
}

async function cmdStats(opts) {
  const db = openStore(opts.db || DB_FILE);
  const series = db.prepare('SELECT * FROM series_meta ORDER BY symbol, series').all();
  const total = db.prepare('SELECT COUNT(*) AS n FROM bars').get();
  say(`总 K 线根数：${total.n}`);
  if (!series.length) {
    say('还没有数据。');
    db.close();
    return 0;
  }
  say('');
  const fmt = (t) => (t ? new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 19) : '-');
  for (const s of series) {
    say(`${s.symbol} · series ${s.series} · ${s.interval || '周期未知'}`);
    say(`   ${s.bars} 根  ${fmt(s.first_t)} → ${fmt(s.last_t)}`);
  }
  db.close();
  return 0;
}

async function cmdExport(opts) {
  const db = openStore(opts.db || DB_FILE);
  const w = makeWriter(db);
  const rows = w.rows({
    symbol: opts.symbol && opts.symbol !== true ? String(opts.symbol) : null,
    limit: opts.limit === undefined ? 0 : Number(opts.limit),
  });
  const out = opts.out ? path.resolve(String(opts.out)) : path.join(ROOT, 'data', 'bars.csv');
  fs.mkdirSync(path.dirname(out), { recursive: true });
  const lines = ['symbol,series,t,iso,o,h,l,c,v'];
  for (const r of rows) {
    lines.push(
      [
        csvEscape(r.symbol),
        csvEscape(r.series),
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
  db.close();
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

const COMMANDS = {
  doctor: cmdDoctor,
  targets: cmdTargets,
  capture: cmdCapture,
  ingest: cmdIngest,
  stats: cmdStats,
  export: cmdExport,
  send: cmdSend,
  eval: cmdEval,
};

const USAGE = `tv —— TradingView WebSocket 抓取与入库

  tv doctor                              诊断 CDP 接入
  tv targets [--match <子串>]             列出可抓标签页
  tv capture [--duration 秒] [--idle 秒] [--reload] [--hook-ws] [--sample N]
  tv ingest [--file a.ndjson ...] [--db 路径]
  tv stats
  tv export [--symbol CME_MINI:MNQ1!] [--limit N] [--out data/bars.csv]
  tv send --frame '<JSON>' [--socket prodata]
  tv eval --expr '<JS>'
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
