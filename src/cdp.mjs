// CDP 接入层
//
// 两条接入路径，按可用性自动选择：
//
//   1) 浏览器级 WebSocket（新版 Chrome 只给这条路）
//      端口和路径来自 <User Data>/DevToolsActivePort：
//        9222
//        /devtools/browser/26fdce33-...
//      Chrome >= 144 启用 chrome://inspect/#remote-debugging 后就是这样：
//      **HTTP 的 /json/version 与 /json/list 返回 404，只剩 WS 端点**。
//      所以不能再靠 HTTP 发现页面，改用 Target.getTargets + Target.attachToTarget。
//
//   2) 传统 HTTP 发现（手动 --user-data-dir 起的独立实例仍然提供）
//
// 注意 Chrome >= 136 起，默认用户数据目录上的 --remote-debugging-port 被静默忽略，
// 只能靠 chrome://inspect 的开关开启，且每个新浏览器会话都要重新勾一次。

import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const BROWSER_PROFILES = {
  chrome: 'Google/Chrome/User Data',
  'chrome-beta': 'Google/Chrome Beta/User Data',
  chromium: 'Chromium/User Data',
  edge: 'Microsoft/Edge/User Data',
};

const FALLBACK_PORTS = [9222, 9229, 9333];

function localAppData() {
  return process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local');
}

export function devToolsPortFiles() {
  const base = localAppData();
  return Object.entries(BROWSER_PROFILES).map(([id, rel]) => ({
    id,
    file: path.join(base, rel, 'DevToolsActivePort'),
  }));
}

// DevToolsActivePort 第一行是端口，第二行是浏览器级 WS 路径
export function readDevToolsActivePort() {
  const found = [];
  for (const { id, file } of devToolsPortFiles()) {
    let raw;
    try {
      raw = fs.readFileSync(file, 'utf8');
    } catch {
      continue;
    }
    const lines = raw.trim().split(/\r?\n/).filter(Boolean);
    const port = Number(lines[0]);
    if (Number.isInteger(port) && port > 0 && port < 65536) {
      found.push({ browser: id, port, browserWsPath: lines[1] || null, file });
    }
  }
  return found;
}

export function tcpProbe(port, host = '127.0.0.1', timeoutMs = 1500) {
  return new Promise((resolve) => {
    const socket = net.createConnection(port, host);
    const done = (ok) => {
      clearTimeout(timer);
      socket.destroy();
      resolve(ok);
    };
    const timer = setTimeout(() => done(false), timeoutMs);
    socket.once('connect', () => done(true));
    socket.once('error', () => done(false));
  });
}

export async function jsonGet(url, timeoutMs = 5000) {
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
    if (!res.ok) return null;
    const text = await res.text();
    return text ? JSON.parse(text) : null;
  } catch {
    return null;
  }
}

// 返回 { port, browserWsUrl, kind, version, httpDiscovery, source }
export async function discoverEndpoint({ ports = FALLBACK_PORTS } = {}) {
  const override = process.env.TV_CDP_PORT ? Number(process.env.TV_CDP_PORT) : null;

  const candidates = [];
  if (override) candidates.push({ port: override, source: 'TV_CDP_PORT' });
  for (const x of readDevToolsActivePort()) candidates.push({ ...x, source: 'DevToolsActivePort' });
  for (const p of ports) if (!candidates.some((c) => c.port === p)) candidates.push({ port: p, source: 'probe' });

  const tried = [];
  for (const c of candidates) {
    if (!(await tcpProbe(c.port))) {
      tried.push({ port: c.port, why: '端口无监听' });
      continue;
    }

    // 新版 Chrome 上这一条会 404，属于预期，不是错误
    const version = await jsonGet(`http://127.0.0.1:${c.port}/json/version`);
    let browserWsPath = c.browserWsPath || null;
    if (!browserWsPath && version?.webSocketDebuggerUrl) {
      try {
        browserWsPath = new URL(version.webSocketDebuggerUrl).pathname;
      } catch {}
    }

    if (browserWsPath) {
      return {
        ...c,
        browserWsUrl: `ws://127.0.0.1:${c.port}${browserWsPath}`,
        kind: 'browser-ws',
        version,
        httpDiscovery: Boolean(version),
      };
    }
    tried.push({ port: c.port, why: '端口在监听，但拿不到浏览器级 WS 路径' });
  }

  const err = new Error('未发现可用的 Chrome 调试端点');
  err.tried = tried;
  err.portFiles = readDevToolsActivePort();
  throw err;
}

// 传统 HTTP 发现；拿不到返回 null，由调用方回退到 Target.getTargets
export async function listTargetsHttp(port) {
  const list = await jsonGet(`http://127.0.0.1:${port}/json/list`, 5000);
  return Array.isArray(list) ? list : null;
}

export function pageTargets(targets) {
  return (targets || []).filter((t) => t.type === 'page' && (t.id || t.targetId));
}

// 按 URL / 标题子串挑目标，score 越高越优先
export function pickTarget(targets, match) {
  const needles = Array.isArray(match) ? match : [match];
  let best = null;
  for (const t of pageTargets(targets)) {
    const hay = `${t.url || ''} ${t.title || ''}`;
    let score = 0;
    needles.forEach((n, i) => {
      if (n && hay.includes(n)) score += 100 - i * 10;
    });
    if (/tradingview\.com\/chart/i.test(t.url || '')) score += 20;
    if (score > 0 && (!best || score > best.score)) best = { target: t, score };
  }
  return best?.target || null;
}

// 一条 WebSocket 上复用多个 CDP 会话（flatten 模式）。
// 会话 = (sessionId, method) -> 回调 的过滤视图。
class Transport {
  #ws = null;
  #seq = 0;
  #pending = new Map();
  #handlers = new Map();

  static connect(wsUrl, { timeoutMs = 25000, label = 'cdp' } = {}) {
    const t = new Transport();
    return t.#open(wsUrl, timeoutMs, label).then(() => t);
  }

  #open(wsUrl, timeoutMs, label) {
    return new Promise((resolve, reject) => {
      let settled = false;
      const timer = setTimeout(() => {
        if (settled) return;
        settled = true;
        reject(
          new Error(
            `${label}: 连接 CDP 超时（${timeoutMs}ms）。` +
              '若 Chrome 弹出了 "Allow remote debugging?" 对话框，请在浏览器里点 Allow 后重试。',
          ),
        );
      }, timeoutMs);

      const ws = new WebSocket(wsUrl);
      this.#ws = ws;

      ws.onopen = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve();
      };

      ws.onerror = (ev) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        reject(new Error(`${label}: CDP 连接失败（${ev?.message || 'error'}）`));
      };

      ws.onmessage = (ev) => this.#onMessage(ev.data);

      ws.onclose = () => {
        for (const [, p] of this.#pending) p.reject(new Error(`${label}: 连接已关闭`));
        this.#pending.clear();
        this.#dispatch(null, '__closed', {});
      };
    });
  }

  #onMessage(data) {
    let msg;
    try {
      msg = JSON.parse(typeof data === 'string' ? data : String(data));
    } catch {
      return;
    }
    if (msg.id != null && this.#pending.has(msg.id)) {
      const p = this.#pending.get(msg.id);
      this.#pending.delete(msg.id);
      if (msg.error) p.reject(new Error(`${p.method} 失败: ${msg.error.message}`));
      else p.resolve(msg.result);
      return;
    }
    if (msg.method) this.#dispatch(msg.sessionId ?? null, msg.method, msg.params || {});
  }

  #dispatch(sessionId, method, params) {
    const hs = this.#handlers.get(`${sessionId ?? ''}\u0000${method}`);
    if (!hs) return;
    for (const h of hs) {
      try {
        h(params);
      } catch (e) {
        process.stderr.write(`[cdp] ${method} 处理器异常: ${e.message}\n`);
      }
    }
  }

  send(sessionId, method, params = {}, timeoutMs = 60000) {
    const id = ++this.#seq;
    const frame = { id, method, params };
    if (sessionId) frame.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new Error(`${method} 超时`));
      }, timeoutMs);
      this.#pending.set(id, {
        method,
        resolve: (r) => {
          clearTimeout(timer);
          resolve(r);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      try {
        this.#ws.send(JSON.stringify(frame));
      } catch (e) {
        this.#pending.delete(id);
        clearTimeout(timer);
        reject(e);
      }
    });
  }

  on(sessionId, method, handler) {
    const key = `${sessionId ?? ''}\u0000${method}`;
    if (!this.#handlers.has(key)) this.#handlers.set(key, new Set());
    this.#handlers.get(key).add(handler);
    return () => this.#handlers.get(key)?.delete(handler);
  }

  close() {
    try {
      this.#ws?.close();
    } catch {}
  }
}

export class CdpSession {
  constructor(transport, label, sessionId = null) {
    this.transport = transport;
    this.label = label;
    this.sessionId = sessionId;
  }

  send(method, params = {}, timeoutMs = 60000) {
    return this.transport.send(this.sessionId, method, params, timeoutMs);
  }

  on(method, handler) {
    return this.transport.on(this.sessionId, method, handler);
  }

  async eval(expression, { awaitPromise = false, returnByValue = true } = {}) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise, returnByValue });
    if (r.exceptionDetails) {
      throw new Error(`页面内求值异常: ${r.exceptionDetails.text} ${r.result?.description || ''}`);
    }
    return r.result?.value;
  }
}

export class CdpBrowser {
  constructor(transport, session, endpoint) {
    this.transport = transport;
    this.session = session;
    this.endpoint = endpoint;
  }

  static async launch({ endpoint = null } = {}) {
    const ep = endpoint || (await discoverEndpoint());
    const transport = await Transport.connect(ep.browserWsUrl, { label: 'browser' });
    return new CdpBrowser(transport, new CdpSession(transport, 'browser', null), ep);
  }

  // 统一拿页面列表：HTTP 发现可用就用，否则走 Target.getTargets
  async targets() {
    if (this.endpoint.httpDiscovery) {
      const viaHttp = await listTargetsHttp(this.endpoint.port);
      if (viaHttp) return viaHttp;
    }
    const r = await this.session.send('Target.getTargets');
    return (r?.targetInfos || []).map((t) => ({
      id: t.targetId,
      targetId: t.targetId,
      type: t.type,
      title: t.title,
      url: t.url,
      attached: t.attached,
    }));
  }

  async attach(target) {
    const targetId = target.targetId || target.id;
    const { sessionId } = await this.session.send('Target.attachToTarget', { targetId, flatten: true });
    return new CdpSession(this.transport, `page:${String(targetId).slice(0, 8)}`, sessionId);
  }

  async detach(session) {
    if (!session?.sessionId) return;
    try {
      await this.session.send('Target.detachFromTarget', { sessionId: session.sessionId });
    } catch {}
  }

  close() {
    this.transport.close();
  }
}

// 便捷入口：发现端点 -> 连浏览器级 -> 挑中页面 -> attach 出可用会话
export async function openPage({ match = 'tradingview.com/chart', endpoint = null } = {}) {
  const browser = await CdpBrowser.launch({ endpoint });
  const targets = await browser.targets();
  const target = pickTarget(targets, match);
  if (!target) {
    const listing = pageTargets(targets)
      .slice(0, 12)
      .map((t) => `  - ${t.title}\n    ${t.url}`)
      .join('\n');
    browser.close();
    const e = new Error(
      `没找到匹配 ${JSON.stringify(match)} 的标签页。\n当前页面：\n${listing || '  (无)'}\n` +
        '请确认 TradingView 图表页在同一个 Chrome 实例里打开着。',
    );
    e.targets = targets;
    throw e;
  }
  const session = await browser.attach(target);
  return { browser, session, target, endpoint: browser.endpoint, targets };
}
