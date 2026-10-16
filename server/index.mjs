#!/usr/bin/env node
// 本地工作台服务：静态页面 + REST 接口 + SSE 实时事件
//
// 只用 node:http，不引入任何第三方依赖 —— 这是个本机工具，
// 用户应该 `node server/index.mjs` 就能开，不该先卡在 npm install 上。
//
// 默认只绑 127.0.0.1。这个服务能操作你已登录的浏览器，暴露到局域网是件危险的事，
// 想远程用请自己走 SSH 端口转发，不要图省事改成 0.0.0.0。

import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { CaptureSession, DEFAULT_SETTINGS } from '../src/session.mjs';
import { migrateLegacy, legacyStatus } from '../src/migrate.mjs';
import { discoverEndpoint, CdpBrowser, pageTargets, tcpProbe, devToolsPortFiles } from '../src/cdp.mjs';
import { pickChangedFiles, readLedger, runIngest, writeLedger, listRawFiles } from '../src/ingest.mjs';
import {
  listSymbols,
  coverageFor,
  calendarFor,
  barsFor,
  exportCsv,
  rawFilesView,
  registryInfo,
} from './views.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const WEB_DIR = path.join(ROOT, 'web');
const SETTINGS_FILE = path.join(ROOT, 'data', 'ui-settings.json');

// ---------------------------------------------------------------- 参数

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--')) out[key] = true;
      else {
        out[key] = next;
        i++;
      }
    }
  }
  return out;
}

const args = parseArgs(process.argv.slice(2));
const PORT = Number(args.port || process.env.TV_PORT || 8787);
const HOST = String(args.host || '127.0.0.1');
const OPEN = Boolean(args.open);

// ---------------------------------------------------------------- 设置持久化

function loadSettings() {
  try {
    const raw = JSON.parse(fs.readFileSync(SETTINGS_FILE, 'utf8'));
    return { ...DEFAULT_SETTINGS, ...raw };
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

function saveSettings(s) {
  fs.mkdirSync(path.dirname(SETTINGS_FILE), { recursive: true });
  fs.writeFileSync(SETTINGS_FILE, JSON.stringify(s, null, 2) + '\n');
  return s;
}

const settings = loadSettings();

// ---------------------------------------------------------------- 事件总线

const sseClients = new Set();

function broadcast(type, data) {
  const payload = `event: ${type}\ndata: ${JSON.stringify(data)}\n\n`;
  for (const res of sseClients) {
    try {
      res.write(payload);
    } catch {
      sseClients.delete(res);
    }
  }
}

const session = new CaptureSession({ root: ROOT, settings });
session.on('log', (line) => broadcast('log', line));
session.on('state', (s) => broadcast('state', s));
session.on('step', (s) => broadcast('step', s));
session.on('driver-done', (s) => broadcast('driver', s));

// ---------------------------------------------------------------- 小工具

function sendJson(res, code, obj) {
  const body = JSON.stringify(obj ?? null);
  res.writeHead(code, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  });
  res.end(body);
}

function readBody(req, limit = 4 * 1024 * 1024) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', (c) => {
      size += c.length;
      if (size > limit) {
        reject(new Error('请求体过大'));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8');
      if (!raw.trim()) return resolve({});
      try {
        resolve(JSON.parse(raw));
      } catch (e) {
        reject(new Error(`请求体不是合法 JSON：${e.message}`));
      }
    });
    req.on('error', reject);
  });
}

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.map': 'application/json; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
  '.csv': 'text/csv; charset=utf-8',
};

function serveStatic(req, res, urlPath) {
  let rel = decodeURIComponent(urlPath.split('?')[0]);
  if (rel === '/' || rel === '') rel = '/index.html';
  // 防止路径穿越
  const abs = path.resolve(WEB_DIR, '.' + rel);
  if (!abs.startsWith(WEB_DIR)) {
    res.writeHead(403).end('forbidden');
    return;
  }
  fs.stat(abs, (err, st) => {
    if (err || !st.isFile()) {
      res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' }).end('404 Not Found');
      return;
    }
    const ext = path.extname(abs).toLowerCase();
    res.writeHead(200, {
      'Content-Type': MIME[ext] || 'application/octet-stream',
      'Content-Length': st.size,
      // 本机工具：不要缓存，改了立刻能看到，省得跟浏览器缓存斗智斗勇
      'Cache-Control': 'no-store',
    });
    fs.createReadStream(abs).pipe(res);
  });
}

// ---------------------------------------------------------------- API

async function api(req, res, url) {
  const { pathname } = url;
  const method = req.method || 'GET';
  // 注意：new URL().pathname 不做百分号解码（品种名里有 : ! 这类字符会被编码成 %3A %21），
  // 而 decodeURIComponent 对已经是明文的 '!' 又是安全的 —— 两种写法都得能吃。
  const seg = pathname
    .replace(/^\/api\//, '')
    .split('/')
    .filter(Boolean)
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    });

  // ---- 状态与设置 ----
  if (pathname === '/api/health') {
    return sendJson(res, 200, { ok: true, root: ROOT, pid: process.pid, node: process.version });
  }

  if (pathname === '/api/state') {
    const reg = legacyStatus(ROOT);
    return sendJson(res, 200, {
      session: session.snapshot(),
      settings,
      defaults: DEFAULT_SETTINGS,
      legacy: { exists: reg.exists, pending: reg.exists && !reg.unchanged },
      logs: session.logs(200),
      now: Date.now(),
    });
  }

  if (pathname === '/api/logs') {
    return sendJson(res, 200, { logs: session.logs(Number(url.searchParams.get('limit') || 300)) });
  }

  if (pathname === '/api/settings') {
    if (method === 'PUT' || method === 'POST') {
      const body = await readBody(req);
      const next = { ...settings };
      for (const [k, v] of Object.entries(body || {})) {
        if (k in DEFAULT_SETTINGS) next[k] = v;
      }
      // 运行中只允许改与"动作"有关、会话能热生效的项，避免出现"界面改了但不生效"
      if (session.state === 'running' || session.state === 'connecting') {
        // 就地改写而不是替换对象：驱动器持有的是同一个引用，
        // 换对象的话它读到的永远是启动那一刻的快照。
        Object.assign(session.settings, pickHot(next));
      } else {
        Object.assign(settings, next);
        saveSettings(settings);
        session.settings = { ...session.settings, ...next };
      }
      return sendJson(res, 200, { settings, session: session.snapshot() });
    }
    return sendJson(res, 200, { settings, defaults: DEFAULT_SETTINGS });
  }

  // ---- 会话控制 ----
  if (pathname === '/api/session/start' && method === 'POST') {
    const body = await readBody(req);
    const overrides = {};
    for (const [k, v] of Object.entries(body || {})) {
      if (k in DEFAULT_SETTINGS) overrides[k] = v;
    }
    Object.assign(settings, overrides);
    saveSettings(settings);
    session.start(overrides).catch(() => {});
    // 立刻回应，不让前端等整个会话结束
    await new Promise((r) => setTimeout(r, 150));
    return sendJson(res, 200, { ok: true, session: session.snapshot() });
  }

  if (pathname === '/api/session/stop' && method === 'POST') {
    const ok = session.stop('web');
    return sendJson(res, 200, { ok, session: session.snapshot() });
  }

  if (pathname === '/api/session/autopull' && method === 'POST') {
    const body = await readBody(req);
    const on = Boolean(body?.on);
    session.setAutoPull(on);
    settings.autoPull = on;
    saveSettings(settings);
    return sendJson(res, 200, { ok: true, autoPull: session.autoPull, session: session.snapshot() });
  }

  // ---- 诊断 ----
  if (pathname === '/api/doctor') {
    const result = await runDoctor();
    return sendJson(res, 200, result);
  }

  if (pathname === '/api/targets') {
    try {
      const ep = await discoverEndpoint();
      const browser = await CdpBrowser.launch({ endpoint: ep });
      try {
        const targets = await browser.targets();
        return sendJson(res, 200, {
          ok: true,
          port: ep.port,
          targets: pageTargets(targets).map((t) => ({
            id: t.id || t.targetId,
            title: t.title,
            url: t.url,
            isChart: /tradingview\.com\/chart/.test(t.url || ''),
          })),
        });
      } finally {
        browser.close();
      }
    } catch (e) {
      return sendJson(res, 200, { ok: false, error: e.message });
    }
  }

  // ---- 品种数据 ----
  if (pathname === '/api/symbols') {
    return sendJson(res, 200, { symbols: listSymbols(ROOT), registry: registryInfo(ROOT) });
  }

  if (seg[0] === 'symbols' && seg[1]) {
    const slug = seg[1];
    const sub = seg[2];

    if (!sub) {
      const sym = listSymbols(ROOT).find((s) => s.slug === slug);
      if (!sym) return sendJson(res, 404, { error: `没有 ${slug} 这个品种库` });
      return sendJson(res, 200, { symbol: sym });
    }

    if (sub === 'coverage') {
      const r = coverageFor(ROOT, slug, {
        top: Number(url.searchParams.get('top') || 40),
        minConfidence: Number(url.searchParams.get('minConfidence') || 0.6),
      });
      if (!r) return sendJson(res, 404, { error: '这个品种库里还没有数据' });
      return sendJson(res, 200, r);
    }

    if (sub === 'calendar') {
      const r = calendarFor(ROOT, slug, {
        windowDays: Number(url.searchParams.get('windowDays') || 0),
        day: url.searchParams.get('day') || null,
      });
      return sendJson(res, 200, r);
    }

    if (sub === 'bars') {
      const r = barsFor(ROOT, slug, {
        limit: Number(url.searchParams.get('limit') || 2000),
        from: url.searchParams.get('from') ? Number(url.searchParams.get('from')) : null,
        to: url.searchParams.get('to') ? Number(url.searchParams.get('to')) : null,
        interval: url.searchParams.get('interval') || null,
      });
      return sendJson(res, 200, r);
    }

    if (sub === 'export' && method === 'POST') {
      const r = exportCsv(ROOT, slug);
      return sendJson(res, 200, r);
    }
  }

  // ---- 原始文件与入库 ----
  if (pathname === '/api/raw') {
    return sendJson(res, 200, {
      files: rawFilesView(ROOT, { symbol: url.searchParams.get('symbol') || null }),
    });
  }

  if (pathname === '/api/ingest' && method === 'POST') {
    const body = await readBody(req);
    const symbol = body?.symbol || null;
    const force = Boolean(body?.all);
    const { files, ledger, all } = pickChangedFiles(ROOT, { symbol, force });
    if (!files.length) {
      return sendJson(res, 200, {
        ok: true,
        started: false,
        message: all.length
          ? `没有新增或变动的抓包文件（共 ${all.length} 个都已入库）`
          : 'data/raw 下还没有抓包文件',
        total: all.length,
      });
    }
    sendJson(res, 200, { ok: true, started: true, files: files.length, total: all.length });
    // 后台跑，进度通过 SSE 推给页面
    (async () => {
      const t0 = Date.now();
      broadcast('log', { at: Date.now(), level: 'info', text: `开始入库 ${files.length} 个文件…` });
      try {
        const r = await runIngest({
          root: ROOT,
          files,
          symbol,
          log: (m) => broadcast('log', { at: Date.now(), level: 'muted', text: m }),
        });
        const nextLedger = { ...ledger };
        for (const f of r.files) {
          nextLedger[f.name] = {
            size: f.size,
            mtimeMs: f.mtimeMs,
            lines: f.lines,
            bars: f.bars,
            at: Date.now(),
          };
        }
        writeLedger(ROOT, nextLedger);
        broadcast('log', {
          at: Date.now(),
          level: 'ok',
          text: `入库完成：${r.summary.bars} 根 K 线，耗时 ${((Date.now() - t0) / 1000).toFixed(1)}s`,
        });
        broadcast('ingested', { summary: { bars: r.summary.bars, frames: r.summary.frames }, bySymbol: r.bySymbol });
      } catch (e) {
        broadcast('log', { at: Date.now(), level: 'error', text: `入库失败：${e.message}` });
      }
    })();
    return;
  }

  // ---- 迁移 ----
  if (pathname === '/api/migrate') {
    const reg = legacyStatus(ROOT);
    if (method === 'POST') {
      const body = await readBody(req);
      const r = migrateLegacy(ROOT, {
        force: Boolean(body?.force),
        log: (m) => broadcast('log', { at: Date.now(), level: 'muted', text: m }),
      });
      broadcast('log', {
        at: Date.now(),
        level: r.migrated ? 'ok' : 'muted',
        text: r.migrated
          ? `迁移完成：${r.symbols.length} 个品种拆分成独立库文件`
          : `未执行迁移（${r.reason}）`,
      });
      return sendJson(res, 200, { ok: true, ...r, symbols: listSymbols(ROOT) });
    }
    return sendJson(res, 200, reg);
  }

  return sendJson(res, 404, { error: `未知接口 ${pathname}` });
}

// 运行中还能热改的字段：只放"影响下一步动作"的。
// symbol / match 不放 —— 它们在会话初始化时就绑定到抓包目录与目标页了，中途改只会造成不一致。
const HOT_FIELDS = [
  'autoPull', 'dx', 'maxSteps', 'stopAfter', 'quietSec',
  'betweenSec', 'maxMinutes', 'restoreView',
];

function pickHot(s) {
  const hot = {};
  for (const k of HOT_FIELDS) if (k in s) hot[k] = s[k];
  return hot;
}

async function runDoctor() {
  const out = {
    node: process.version,
    webSocket: typeof WebSocket === 'function',
    portFiles: devToolsPortFiles().map((p) => ({ ...p, exists: fs.existsSync(p.file) })),
    ports: {},
    ok: false,
    error: null,
    targets: [],
  };
  for (const p of [9222, 9229, 9333]) out.ports[p] = await tcpProbe(p);
  try {
    const ep = await discoverEndpoint();
    out.endpoint = { port: ep.port, url: ep.browserWsUrl, httpDiscovery: ep.httpDiscovery, browser: ep.version?.Browser || null };
    const browser = await CdpBrowser.launch({ endpoint: ep });
    try {
      const targets = await browser.targets();
      out.targets = pageTargets(targets).map((t) => ({
        id: t.id || t.targetId,
        title: t.title,
        url: t.url,
        isChart: /tradingview\.com\/chart/.test(t.url || ''),
      }));
    } finally {
      browser.close();
    }
    out.ok = true;
  } catch (e) {
    out.error = e.message;
    out.tried = e.tried || null;
  }
  return out;
}

// ---------------------------------------------------------------- 路由

const server = http.createServer((req, res) => {
  const url = new URL(req.url || '/', `http://${req.headers.host || '127.0.0.1'}`);

  if (url.pathname === '/api/events') {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream; charset=utf-8',
      'Cache-Control': 'no-cache, no-transform',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });
    res.setTimeout(0);
    res.write('retry: 2000\n\n');
    res.write(`event: hello\ndata: ${JSON.stringify({ session: session.snapshot(), logs: session.logs(200) })}\n\n`);
    sseClients.add(res);
    const ping = setInterval(() => {
      try {
        res.write(`: ping ${Date.now()}\n\n`);
      } catch {}
    }, 15000);
    ping.unref?.();
    const drop = () => {
      clearInterval(ping);
      sseClients.delete(res);
    };
    req.on('close', drop);
    req.on('error', drop);
    return;
  }

  if (url.pathname.startsWith('/api/')) {
    api(req, res, url).catch((e) => {
      if (res.headersSent) {
        try {
          res.end();
        } catch {}
        return;
      }
      sendJson(res, e.status || 500, { error: e.message });
    });
    return;
  }

  serveStatic(req, res, url.pathname);
});

server.keepAliveTimeout = 65000;
server.headersTimeout = 70000;

// ---------------------------------------------------------------- 启动

function banner() {
  const lines = [];
  lines.push('');
  lines.push('  TradingView 抓取工作台');
  lines.push(`  → http://${HOST}:${PORT}`);
  lines.push('');
  lines.push(`  数据目录  ${path.join(ROOT, 'data')}`);
  lines.push(`  品种库    ${path.join(ROOT, 'data', 'db')}（一个品种一个 .sqlite）`);
  lines.push('');
  const reg = legacyStatus(ROOT);
  if (reg.exists && !reg.unchanged) {
    lines.push('  ⚠ 发现旧版混合库 data/bars.sqlite，正在自动拆分…');
  }
  lines.push('  停止服务：Ctrl+C');
  lines.push('');
  process.stdout.write(lines.join('\n') + '\n');
}

// 旧版混合库自动迁移：用户不必先跑任何命令
try {
  const r = migrateLegacy(ROOT, { log: (m) => process.stdout.write(m + '\n') });
  if (!r.migrated && r.reason === 'already-done') {
    // 静默
  }
} catch (e) {
  process.stdout.write(`[warn] 旧库迁移失败：${e.message}\n`);
}

server.listen(PORT, HOST, () => {
  banner();
});

server.on('error', (e) => {
  if (e.code === 'EADDRINUSE') {
    process.stderr.write(`\n端口 ${PORT} 已被占用。换一个：node server/index.mjs --port 8877\n\n`);
    process.exit(1);
  }
  throw e;
});

// ---------------------------------------------------------------- 收尾

let closing = false;
async function shutdown(sig) {
  if (closing) return;
  closing = true;
  process.stdout.write(`\n收到 ${sig}，正在收尾…\n`);
  for (const res of sseClients) {
    try {
      res.write('event: bye\ndata: {}\n\n');
      res.end();
    } catch {}
  }
  sseClients.clear();
  try {
    if (session.state !== 'idle') {
      session.stop('shutdown');
      await Promise.race([session.wait(), new Promise((r) => setTimeout(r, 10000))]);
    }
  } catch {}
  try {
    saveSettings(settings);
  } catch {}
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(0), 1500).unref();
}

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
