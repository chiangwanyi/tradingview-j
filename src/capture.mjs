// 抓帧：接入已登录的 Chrome，订阅该页面的所有 WebSocket 帧，落盘 NDJSON
//
// 特点：
//   - 不改动页面业务逻辑，纯旁路监听
//   - 每个 WS 帧记方向（send/recv）、时间戳、opcode
//   - 二进制帧以 base64 保真保存，避免解码期丢信息
//   - --hook-ws 会在页面里装一个 WebSocket 记录器，之后可以主动发帧

import fs from 'node:fs';
import path from 'node:path';
import { openPage } from './cdp.mjs';
import { splitFrame, symbolFromUrl } from './frames.mjs';

const HOOK_SOURCE = `
(() => {
  if (window.__tvws_installed) return;
  window.__tvws_installed = true;
  window.__tvws = [];
  const Native = window.WebSocket;
  const Wrapped = function (url, protocols) {
    const sock = protocols === undefined ? new Native(url) : new Native(url, protocols);
    try {
      window.__tvws.push(sock);
      if (window.__tvws.length > 50) window.__tvws.shift();
    } catch (e) {}
    return sock;
  };
  Wrapped.prototype = Native.prototype;
  ['CONNECTING', 'OPEN', 'CLOSING', 'CLOSED'].forEach((k) => { Wrapped[k] = Native[k]; });
  window.WebSocket = Wrapped;
  window.__tvFrame = (payload) => {
    const body = typeof payload === 'string' ? payload : JSON.stringify(payload);
    return '~m~' + body.length + '~m~' + body;
  };
  window.__tvSockets = () => window.__tvws.filter((s) => s.readyState === 1).map((s) => s.url);
  window.__tvSend = (payload, urlPart) => {
    const needle = urlPart || 'prodata';
    const sock = window.__tvws.filter((s) => s.readyState === 1 && String(s.url).includes(needle)).pop();
    if (!sock) return { ok: false, error: 'no open socket matching ' + needle };
    const frame = window.__tvFrame(payload);
    sock.send(frame);
    return { ok: true, url: sock.url, bytes: frame.length };
  };
})();
`;

export function defaultRawDir(root) {
  return path.join(root, 'data', 'raw');
}

export async function capture(options = {}) {
  const {
    root = process.cwd(),
    match = ['tradingview.com/chart'],
    durationMs = 0,
    idleStopMs = 0,
    reload = false,
    hookWs = false,
    sampleCount = 0,
    log = (m) => process.stdout.write(m + '\n'),
  } = options;

  const { browser, session, target, endpoint } = await openPage({ match });
  log(
    `已接入调试端点：port ${endpoint.port}` +
      (endpoint.version?.Browser ? ` · ${endpoint.version.Browser}` : '') +
      ` · 来源 ${endpoint.source}` +
      (endpoint.httpDiscovery ? '' : ' · HTTP 发现接口不可用（新版 Chrome 预期行为，走浏览器级 WS）'),
  );
  log(`目标页：${target.title}\n        ${target.url}`);

  if (hookWs) {
    await session.send('Page.enable');
    await session.send('Page.addScriptToEvaluateOnNewDocument', { source: HOOK_SOURCE });
    log('已安装 WebSocket 记录器（刷新后生效，之后可用 tv send 主动发帧）');
  }

  await session.send('Network.enable', {
    maxTotalBufferSize: 200 * 1024 * 1024,
    maxResourceBufferSize: 20 * 1024 * 1024,
  });

  const rawDir = defaultRawDir(root);
  fs.mkdirSync(rawDir, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const outFile = path.join(rawDir, `${stamp}.ndjson`);
  const sampleFile = sampleCount > 0 ? path.join(rawDir, `${stamp}.sample.txt`) : null;
  const stream = fs.createWriteStream(outFile, { flags: 'a' });
  const sample = sampleFile ? fs.createWriteStream(sampleFile, { flags: 'a' }) : null;

  const write = (rec) => stream.write(JSON.stringify(rec) + '\n');

  write({
    k: 'meta',
    at: Date.now(),
    target: { id: target.id, url: target.url, title: target.title },
    symbolHint: symbolFromUrl(target.url),
    endpoint: { port: endpoint.port, browser: endpoint.browser, source: endpoint.source },
    node: process.version,
  });

  const wsUrls = new Map();
  let recv = 0;
  let sent = 0;
  let sampled = 0;
  let lastActivity = Date.now();

  session.on('Network.webSocketCreated', (p) => {
    wsUrls.set(p.requestId, p.url);
    write({ k: 'ws', at: Date.now(), event: 'created', requestId: p.requestId, url: p.url });
    log(`  ↳ 新 WebSocket: ${shorten(p.url)}`);
  });

  session.on('Network.webSocketWillSendHandshakeRequest', (p) => {
    write({
      k: 'ws',
      at: Date.now(),
      event: 'handshake',
      requestId: p.requestId,
      url: wsUrls.get(p.requestId),
      headers: p.request?.headers,
    });
  });

  const onFrame = (dir) => (p) => {
    lastActivity = Date.now();
    const isBinary = p.response?.opcode === 2;
    const payload = p.response?.payloadData ?? '';
    const rec = {
      k: 'frame',
      at: p.timestamp ? Math.round(p.timestamp * 1000) : Date.now(),
      eventAt: Date.now(),
      requestId: p.requestId,
      url: wsUrls.get(p.requestId) || null,
      dir,
      opcode: p.response?.opcode,
      len: payload.length,
    };
    if (isBinary) rec.b64 = payload;
    else rec.text = payload;
    write(rec);

    if (dir === 'recv') recv++;
    else sent++;

    if (sample && sampled < sampleCount && !isBinary) {
      sampled++;
      sample.write(`\n# [${dir}] ${new Date(rec.eventAt).toISOString()} ${shorten(rec.url || '')}\n`);
      for (const seg of splitFrame(payload)) {
        const head =
          seg.kind === 'json'
            ? `${seg.header} m=${seg.value?.m ?? '(none)'}`
            : `${seg.header || ''} ${seg.kind}`;
        sample.write(`${head}\n`);
        sample.write(`${truncate(seg.raw, 1200)}\n`);
      }
    }
  };

  session.on('Network.webSocketFrameReceived', onFrame('recv'));
  session.on('Network.webSocketFrameSent', onFrame('send'));
  session.on('Network.webSocketClosed', (p) => {
    write({ k: 'ws', at: Date.now(), event: 'closed', requestId: p.requestId, url: wsUrls.get(p.requestId) });
    log(`  ↳ WebSocket 关闭: ${shorten(wsUrls.get(p.requestId) || p.requestId)}`);
  });
  session.on('Network.webSocketFrameError', (p) => {
    write({ k: 'ws', at: Date.now(), event: 'error', requestId: p.requestId, message: p.errorMessage });
  });

  if (reload) {
    log('刷新页面以重新建立 WebSocket 连接…');
    await session.send('Page.enable');
    await session.send('Page.reload', { ignoreCache: false });
  }

  log(`开始抓帧 → ${outFile}`);
  log('（若要停止：Ctrl+C）');

  let stopping = false;
  const stop = () => {
    stopping = true;
  };
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);

  // 计时器里的 tick 必须与 clearInterval 同作用域。
  // 之前写成 `.finally(() => clearInterval(tick))` 抛了 ReferenceError，
  // 结果跳过 browser.close()，WebSocket 一直开着导致进程永不退出。
  let stopReason = 'signal';
  try {
    const startedAt = Date.now();
    stopReason = await new Promise((resolve) => {
      const tick = setInterval(() => {
        if (stopping) return finish('signal');
        if (durationMs && Date.now() - startedAt >= durationMs) return finish('duration');
        if (idleStopMs && Date.now() - lastActivity >= idleStopMs) return finish('idle');
      }, 250);
      function finish(why) {
        clearInterval(tick);
        resolve(why);
      }
    });
  } finally {
    process.removeListener('SIGINT', stop);
    process.removeListener('SIGTERM', stop);
    // 无论正常结束还是中途抛错，都必须把 WebSocket 关掉，否则进程不会退出
    try {
      await browser.detach(session);
    } catch {}
    browser.close();
    await new Promise((r) => stream.end(r));
    if (sample) await new Promise((r) => sample.end(r));
  }

  const result = { file: outFile, sampleFile, recv, sent, wsCount: wsUrls.size, target, endpoint };
  log(
    `抓帧结束：收到 ${recv} 帧 / 发出 ${sent} 帧，涉及 ${wsUrls.size} 条 WebSocket 连接\n文件：${outFile}` +
      (sampleFile ? `\n样本：${sampleFile}` : ''),
  );
  return result;
}

function shorten(s, n = 96) {
  return s && s.length > n ? s.slice(0, n) + '…' : s || '';
}

function truncate(s, n) {
  return s && s.length > n ? s.slice(0, n) + `\n…（共 ${s.length} 字符）` : s;
}
