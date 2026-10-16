// 端到端验证：真后端 + 真浏览器。
//
// 为什么必须跑这一遍：
//   - jsdom 没有布局引擎也没有 CSSOM，Tailwind 运行时注入的样式它一概读不到；
//     「两栏有没有并排」「开关是不是 46×26」「canvas 有没有被压成 0 宽」只有真浏览器答得了。
//   - 页面的 JS 报错（比如模板里引用了不存在的字段）也只有真跑才暴露。
//
// 刻意用**独立的无头 Chrome + 临时 profile**，不碰用户自己那个正在登录的浏览器：
//   1. 不去创建/关闭用户可见的标签页；
//   2. 视口可以钉死，响应式断点不会飘；
//   3. 用 TV_CDP_PORT 把服务的调试端点指到这个无头实例，
//      于是「开始抓取」链路也能被真实走一遍，而不需要用户去点 Chrome 的授权弹窗。
//
// 图表页用一个本机 URL 冒充（URL 里含 tradingview.com/chart 就够了 ——
// pickTarget 是按 URL/标题子串匹配的），这样完全不依赖外网和登录态。
//
// 运行：node test/e2e.mjs

import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const NODE = process.execPath;
const PORT = Number(process.env.TV_E2E_PORT || 8795);
const CDP_PORT = Number(process.env.TV_E2E_CDP_PORT || 9223);
const ORIGIN = `http://127.0.0.1:${PORT}`;
const SHOT_DIR = path.join(ROOT, 'data', 'e2e');

// 时间轴（每日覆盖条带）在前端是关着的 —— 对应 web/app.js 里的 showTimeline。
// 这个常量必须跟着它一起改：
//   前端关了、这里还按「开着」断言 → 断言一个不存在的元素，假红；
//   前端恢复了、这里还是 false    → 时间轴整块无人验证，假绿（比不写更危险）。
// 所以两种状态下都保留断言，只是分成两套。
const TIMELINE_ENABLED = false;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let passed = 0;
let failed = 0;

// ---------- 耗时诊断 ----------
//
// e2e 慢的时候必须能一眼看出「慢在哪一段」，否则只能靠猜（"大概是浏览器启动慢"），
// 而猜出来的优化等于没优化。所以分两层打点：
//   - section()：粗粒度，几大段各报一次，附带距上一段的毫秒；
//   - check()：细粒度，单条断言超过 SLOW_MS 才把耗时印出来，免得 38 行全是 (12ms) 的噪音。
const SLOW_MS = 800;
let lastMark = Date.now();
const t0 = Date.now();

function section(name) {
  const now = Date.now();
  console.log(`\n${name}   [距上一段 ${now - lastMark}ms / 累计 ${now - t0}ms]`);
  lastMark = now;
}

// 注意：这里必须是 async 且真的 await fn()。
// 之前写的是同步版，断言回调一旦写成 async，它返回的 Promise 会被直接丢掉，
// 里面的 assert 失败不会冒泡 —— 测试会「假绿」，比不写还危险。
async function check(name, fn, detail) {
  const c0 = Date.now();
  const cost = (n) => (n >= SLOW_MS ? `  (${n}ms)` : '');
  try {
    await fn();
    console.log(`  ok   ${name}${cost(Date.now() - c0)}`);
    passed++;
  } catch (e) {
    console.log(`  FAIL ${name}${cost(Date.now() - c0)}\n       ${e.message}`);
    if (detail !== undefined) console.log(`       实际值：${JSON.stringify(detail)}`);
    failed++;
    process.exitCode = 1;
  }
}

// ---------------------------------------------------------------- 工具

function findBrowser() {
  const cands = [
    'C:/Program Files/Google/Chrome/Application/chrome.exe',
    'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe',
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
    'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
    process.env.LOCALAPPDATA && path.join(process.env.LOCALAPPDATA, 'Google/Chrome/Application/chrome.exe'),
    process.env.LOCALAPPDATA && path.join(process.env.LOCALAPPDATA, 'Microsoft/Edge/Application/msedge.exe'),
  ].filter(Boolean);
  for (const c of cands) if (fs.existsSync(c)) return c;
  return null;
}

async function waitHttp(url, { tries = 80, gap = 250, timeout = 2000 } = {}) {
  for (let i = 0; i < tries; i++) {
    try {
      const r = await fetch(url, { signal: AbortSignal.timeout(timeout) });
      if (r.ok) return await r.json().catch(() => ({}));
    } catch {}
    await sleep(gap);
  }
  throw new Error(`${url} 一直没起来`);
}

async function api(pathname, init) {
  const r = await fetch(ORIGIN + pathname, init);
  const text = await r.text();
  try {
    return JSON.parse(text);
  } catch {
    return { _raw: text };
  }
}

// 极简 CDP 客户端
function makeCdp(wsUrl) {
  const ws = new WebSocket(wsUrl);
  let seq = 0;
  const pending = new Map();
  const events = [];
  const listeners = new Map();

  const ready = new Promise((res, rej) => {
    ws.onopen = () => res();
    ws.onerror = (e) => rej(new Error('CDP 连接失败: ' + (e?.message || 'error')));
  });

  ws.onmessage = (ev) => {
    let m;
    try {
      m = JSON.parse(typeof ev.data === 'string' ? ev.data : String(ev.data));
    } catch {
      return;
    }
    if (m.id && pending.has(m.id)) {
      const p = pending.get(m.id);
      pending.delete(m.id);
      if (m.error) p.reject(new Error(`${p.method}: ${m.error.message}`));
      else p.resolve(m.result);
      return;
    }
    if (m.method) {
      events.push(m);
      const set = listeners.get(m.method);
      if (set) for (const fn of set) fn(m.params, m.sessionId);
    }
  };

  const send = (method, params = {}, sessionId = null) =>
    new Promise((resolve, reject) => {
      const id = ++seq;
      const frame = { id, method, params };
      if (sessionId) frame.sessionId = sessionId;
      pending.set(id, { resolve, reject, method });
      ws.send(JSON.stringify(frame));
    });

  const on = (method, fn) => {
    if (!listeners.has(method)) listeners.set(method, new Set());
    listeners.get(method).add(fn);
  };

  return { ws, ready, send, on, events, close: () => { try { ws.close(); } catch {} } };
}

// 在一个 page target 上做断言的包装
function page(send, sessionId) {
  const ev = async (expression, timeoutMs = 20000) => {
    const r = await send(
      'Runtime.evaluate',
      { expression, returnByValue: true, awaitPromise: true },
      sessionId,
    );
    if (r.exceptionDetails) {
      throw new Error(`页面内求值异常：${r.exceptionDetails.text} ${r.result?.description || ''}`);
    }
    return r.result?.value;
  };
  const json = async (expression) => {
    const v = await ev(expression);
    return typeof v === 'string' ? JSON.parse(v) : v;
  };
  const until = async (expression, { tries = 80, gap = 200, label = expression } = {}) => {
    for (let i = 0; i < tries; i++) {
      try {
        if (await ev(expression)) return true;
      } catch {}
      await sleep(gap);
    }
    throw new Error(`等待超时：${label}`);
  };
  return { ev, json, until };
}

// ---------------------------------------------------------------- 主流程

const browserExe = findBrowser();
if (!browserExe) {
  console.error('本机没找到 Chrome/Edge，跳过浏览器验证。');
  process.exit(0);
}

fs.mkdirSync(SHOT_DIR, { recursive: true });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'tv-e2e-profile-'));
const serverLog = fs.createWriteStream(path.join(SHOT_DIR, 'server.log'));
let server = null;
let chrome = null;
let cdp = null;

async function cleanup() {
  try {
    cdp?.close();
  } catch {}
  try {
    chrome?.kill();
  } catch {}
  // 无头 Chrome 会派生子进程（renderer/gpu/utility），主进程 kill 之后它们可能留下来，
  // 所以要按 profile 目录兜底清一遍。
  //
  // 为什么不能按 IMAGENAME 过滤：
  //   taskkill /F /T /FI "IMAGENAME eq chrome.exe" 会命中**用户自己那个正在登录的 Chrome**，
  //   而且是强杀 —— 未保存的分页全丢。测试没有权力动用户日常在用的浏览器。
  // 所以改成按本轮那次 mkdtemp 出来的临时 profile 路径精确匹配命令行。
  try {
    const pat = profile.replace(/'/g, "''");
    const ps =
      `Get-CimInstance Win32_Process -Filter "Name='${path.basename(browserExe)}'" | ` +
      `Where-Object { $_.CommandLine -like '*${pat}*' } | ` +
      `ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`;
    await new Promise((res) => {
      const k = spawn('powershell', ['-NoProfile', '-NonInteractive', '-Command', ps], {
        stdio: 'ignore',
        shell: false,
      });
      k.on('exit', res);
      k.on('error', res);
    });
  } catch {}
  try {
    server?.kill('SIGTERM');
  } catch {}
  await sleep(600);
  try {
    server?.kill('SIGKILL');
  } catch {}
  try {
    fs.rmSync(profile, { recursive: true, force: true });
  } catch {}
}

try {
  // ---- 1. 起无头浏览器 ----
  console.log(`浏览器：${browserExe}`);
  chrome = spawn(
    browserExe,
    [
      '--headless=new',
      '--disable-gpu',
      '--hide-scrollbars',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-extensions',
      '--disable-background-timer-throttling',
      '--disable-renderer-backgrounding',
      '--disable-backgrounding-occluded-windows',
      `--remote-debugging-port=${CDP_PORT}`,
      `--user-data-dir=${profile}`,
      '--window-size=1500,1000',
      'about:blank',
    ],
    { stdio: 'ignore' },
  );

  const version = await waitHttp(`http://127.0.0.1:${CDP_PORT}/json/version`, { tries: 100 });
  console.log(`CDP：${version.Browser}`);
  const browserWs = version.webSocketDebuggerUrl;

  cdp = makeCdp(browserWs);
  await cdp.ready;

  // 注意：不能给 createTarget 传 width/height —— Chrome 155 会回
  // "Target position can only be set for new windows" 直接报错。
  // 视口尺寸统一由后面的 Emulation.setDeviceMetricsOverride 钉死。
  const { targetId: fbTarget } = await cdp.send('Target.createTarget', { url: 'about:blank' });

  // ---- 2. 起服务，并把调试端点指向这个无头实例 ----
  server = spawn(NODE, ['server/index.mjs', '--port', String(PORT)], {
    cwd: ROOT,
    env: { ...process.env, TV_CDP_PORT: String(CDP_PORT) },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  server.stdout.pipe(serverLog);
  server.stderr.pipe(serverLog);
  await waitHttp(`${ORIGIN}/api/health`);
  console.log(`服务：${ORIGIN}`);
  section('服务启动完成（含前面的浏览器启动 + CDP 就绪）');

  // ---- 3. 后端 HTTP 断言 ----
  section('后端接口断言');
  const health = await api('/api/health');
  await check('/api/health 起来且报告根目录与 pid', () => {
    assert.equal(health.ok, true);
    assert.ok(health.pid > 0);
    assert.ok(String(health.root).toLowerCase() === ROOT.toLowerCase());
  }, health);

  const symbols = await api('/api/symbols');
  await check('/api/symbols 至少有一个品种库，且根数 > 0', () => {
    assert.ok(Array.isArray(symbols.symbols));
    assert.ok(symbols.symbols.length >= 1);
    assert.ok(symbols.symbols[0].bars > 0);
  }, symbols.symbols?.map((s) => ({ slug: s.slug, bars: s.bars })));

  const slug = symbols.symbols[0]?.slug;
  const cal = await api(`/api/symbols/${encodeURIComponent(slug)}/calendar`);
  await check('覆盖日历：只有 states 长度恒等于 slotsPerDay 才画得出来', () => {
    assert.ok(cal.slotsPerDay > 0);
    assert.ok(cal.days.length > 0);
    for (const d of cal.days) assert.equal(d.states.length, cal.slotsPerDay);
    assert.equal(cal.histogram.length, cal.slotsPerDay);
  }, { slotsPerDay: cal.slotsPerDay, days: cal.days.length });

  await check('覆盖日历：每格状态只出现 0/1/2/3 四种字符', () => {
    const bad = new Set();
    for (const d of cal.days) for (const ch of d.states) if (!'0123'.includes(ch)) bad.add(ch);
    assert.equal(bad.size, 0);
  }, [...new Set(cal.days.flatMap((d) => [...d.states]))].join(''));

  // 月历会把整月都画成格子（含数据没覆盖到的日子），所以 ?day= 完全可能落在 cal.days 之外。
  // 这种日子也必须给一个明确的 focus，否则页面会自相矛盾。
  const farDay = await api(`/api/symbols/${encodeURIComponent(slug)}/calendar?day=1999-01-01`);
  await check('问一个范围外的日子，接口也给明确答复（focus.status=out-of-range）', () => {
    assert.ok(farDay.focus, 'focus 缺了，前端只能干显示占位文案');
    assert.equal(farDay.focus.iso, '1999-01-01');
    assert.equal(farDay.focus.status, 'out-of-range');
    assert.equal(farDay.focus.n, 0);
    assert.deepEqual(farDay.focus.segments, []);
  }, farDay.focus);

  const inRangeDay = await api(
    `/api/symbols/${encodeURIComponent(slug)}/calendar?day=${encodeURIComponent(cal.days[0].iso)}`,
  );
  await check('问一个范围内的日子，focus 带出根数与连续时段', () => {
    assert.ok(inRangeDay.focus);
    assert.equal(inRangeDay.focus.iso, cal.days[0].iso);
    assert.ok(inRangeDay.focus.n > 0, `n=${inRangeDay.focus.n}`);
    assert.ok(inRangeDay.focus.segments.length > 0, '有数据却给不出任何连续时段');
  }, { iso: inRangeDay.focus?.iso, n: inRangeDay.focus?.n, segs: inRangeDay.focus?.segments });

  const cov = await api(`/api/symbols/${encodeURIComponent(slug)}/coverage`);
  await check('覆盖度：断档条目都带人看的 ISO 标签（不能只有时间戳）', () => {
    assert.ok(cov.breaks.length > 0 || cov.breakCount === 0);
    for (const b of cov.breaks) {
      // fmtTs 给到秒，所以别把正则收紧到 HH:MM
      assert.match(b.fromIso, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?$/);
      assert.match(b.toIso, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?$/);
    }
  }, cov.breaks?.[0]);

  const notFound = await api('/api/symbols/不存在的品种/calendar');
  await check('未知品种给 404 语义而不是崩掉', () => {
    assert.ok(notFound.error);
  }, notFound);

  const raw = await api('/api/raw');
  await check('/api/raw 能给每个抓包文件配到品种', () => {
    assert.ok(raw.files.length > 0);
    assert.ok(raw.files.some((f) => f.symbol));
  }, raw.files.slice(0, 3).map((f) => ({ name: f.name, symbol: f.symbol, pending: f.pending })));

  // ---- 4. 打开工作台页面 ----
  section('浏览器里的界面');
  await cdp.send('Target.activateTarget', { targetId: fbTarget });
  const { sessionId: pageSess } = await cdp.send('Target.attachToTarget', {
    targetId: fbTarget,
    flatten: true,
  });
  const P = page(cdp.send, pageSess);

  const pageErrors = [];
  const consoleErrors = [];
  cdp.on('Runtime.exceptionThrown', (p, sid) => {
    if (sid === pageSess) pageErrors.push(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text);
  });
  cdp.on('Runtime.consoleAPICalled', (p, sid) => {
    if (sid !== pageSess) return;
    if (p.type === 'error' || p.type === 'warning') {
      consoleErrors.push(`${p.type}: ${(p.args || []).map((a) => a.value ?? a.description ?? '').join(' ')}`);
    }
  });
  // 浏览器自己的日志通道（资源 404、CSS 解析失败之类不会走 console，只会走这里）
  const browserLogs = [];
  cdp.on('Log.entryAdded', (p, sid) => {
    if (sid !== pageSess) return;
    if (p.entry?.level === 'error') browserLogs.push(`${p.entry.source}: ${p.entry.text}`);
  });

  await cdp.send('Runtime.enable', {}, pageSess);
  await cdp.send('Page.enable', {}, pageSess);
  await cdp.send('Log.enable', {}, pageSess);
  await cdp.send('Emulation.setDeviceMetricsOverride', {
    width: 1500,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  }, pageSess);

  await cdp.send('Page.navigate', { url: ORIGIN + '/' }, pageSess);

  // #app.ready 是 Vue 挂载 + Tailwind 编译完成的标志（style.css 里拿它做淡入）
  await P.until("document.getElementById('app') && document.getElementById('app').classList.contains('ready')", {
    tries: 120,
    gap: 200,
    label: '页面 ready',
  });
  await sleep(700);
  section('页面加载完成（navigate -> #app.ready）');

  await check('页面没有未捕获的 JS 异常', () => {
    assert.equal(pageErrors.length, 0, pageErrors.join(' | '));
  }, pageErrors);

  await check('控制台没有 error 级别输出', () => {
    assert.equal(consoleErrors.filter((x) => x.startsWith('error')).length, 0, consoleErrors.join(' | '));
  }, consoleErrors);

  await check('Tailwind 真的编译了：body 的 font-size 是 14px 而不是默认 16px', async () => {
    const fs14 = await P.ev("getComputedStyle(document.body).fontSize");
    assert.equal(fs14, '14px', `实际 ${fs14}`);
  });

  await check('没有资源加载失败（CSS/JS 404 只会出现在浏览器日志里，控制台看不到）', () => {
    const bad = browserLogs.filter((x) => !/favicon/i.test(x));
    assert.equal(bad.length, 0, bad.join(' | '));
  }, browserLogs);

  // canvas 是用 getComputedStyle 读主题变量的，读不到就拿到空串，而 ctx.fillStyle=''
  // 是静默忽略 —— 图形会被整段跳过，画布上看着"就是空的"，极难排查。
  // 所以这里直接把每个变量都点名查一遍。（根因：Tailwind 4 默认 tree-shake 没被工具类用到的主题变量）
  const themeVars = await P.json(`JSON.stringify((() => {
    const cs = getComputedStyle(document.documentElement);
    const names = ['--color-bg', '--color-panel', '--color-panel2', '--color-line', '--color-line2',
      '--color-ink', '--color-muted', '--color-faint', '--color-accent', '--color-accent2',
      '--color-accentbg', '--color-ok', '--color-okbg', '--color-warn', '--color-warnbg',
      '--color-bad', '--color-badbg'];
    const out = {};
    for (const n of names) out[n] = cs.getPropertyValue(n).trim();
    return out;
  })())`);

  await check('主题变量全都真的进了样式表（缺一个，canvas 就会静默少画一层）', () => {
    const missing = Object.entries(themeVars).filter(([, v]) => !v).map(([k]) => k);
    assert.equal(missing.length, 0, `缺失：${missing.join('、')} —— @theme 是不是漏了 static`);
  }, themeVars);

  // ---- 5. 布局几何断言 ----
  const geo = await P.json(`JSON.stringify((() => {
    const q = (s) => document.querySelector(s);
    const header = q('header');
    const main = q('main');
    const control = q('[data-testid=panel-control]');
    const grid = getComputedStyle(control);
    const sw = q('[data-testid=autopull]');
    const logbox = q('[data-testid=logbox]');
    const tabs = [...document.querySelectorAll('[data-testid^=tab-]')];
    return {
      headerSticky: getComputedStyle(header).position,
      headerTop: header.getBoundingClientRect().top,
      mainTop: main.getBoundingClientRect().top,
      gridCols: grid.gridTemplateColumns,
      controlWidth: control.getBoundingClientRect().width,
      switchW: sw ? Math.round(sw.getBoundingClientRect().width) : 0,
      switchH: sw ? Math.round(sw.getBoundingClientRect().height) : 0,
      logRight: logbox ? Math.round(logbox.getBoundingClientRect().right) : 0,
      tabs: tabs.map(t => t.textContent.trim()),
      bodyScrollW: document.body.scrollWidth,
      winW: window.innerWidth,
    };
  })())`);

  await check('顶栏是 sticky 且贴在视口顶部', () => {
    assert.equal(geo.headerSticky, 'sticky');
    assert.ok(Math.abs(geo.headerTop) <= 1, `headerTop=${geo.headerTop}`);
  }, geo);

  await check('抓取控制页是两栏栅格（左控制/右日志），不是竖着堆', () => {
    // 1500px 视口下 xl:grid-cols-[minmax(0,1fr)_460px] 应当生效 -> 两列
    const cols = geo.gridCols.split(' ').filter(Boolean);
    assert.equal(cols.length, 2, `实际列数 ${cols.length}：${geo.gridCols}`);
    assert.ok(cols[1].endsWith('460px') || Math.abs(parseFloat(cols[1]) - 460) < 2, geo.gridCols);
  }, geo.gridCols);

  await check('自动回拉开关是 46×26 的胶囊（Tailwind 没生成的类会塌成 0 宽）', () => {
    assert.ok(geo.switchW >= 40, `宽 ${geo.switchW}`);
    assert.ok(geo.switchH >= 22, `高 ${geo.switchH}`);
  }, geo);

  await check('三个页签都在，且文案正确', () => {
    assert.deepEqual(geo.tabs, ['抓取控制', '覆盖视图', '数据管理']);
  }, geo.tabs);

  await check('没有横向溢出（scrollWidth 不超过视口 + 1）', () => {
    assert.ok(geo.bodyScrollW <= geo.winW + 1, `scrollWidth=${geo.bodyScrollW} winW=${geo.winW}`);
  }, geo);

  await shot(cdp, pageSess, '01-control.png');

  // ---- 6. 交互：开始抓取 ----
  section('交互：会话启停');
  // 先造一个"图表页"：URL 里含 tradingview.com/chart 就会被匹配到。
  // 用本机地址冒充，完全不依赖外网和登录态。
  const fakeChart = `${ORIGIN}/fake/tradingview.com/chart/EuR5QG29/?symbol=CME_MINI%3AMNQ1%21`;
  await cdp.send('Target.createTarget', { url: fakeChart });
  await sleep(500);

  const targets = await api('/api/targets');
  await check('诊断能看到那个图表页', () => {
    assert.equal(targets.ok, true, targets.error);
    assert.ok(targets.targets.some((t) => t.isChart), JSON.stringify(targets.targets));
  }, targets.targets);

  await P.ev("document.querySelector('[data-testid=start]').click()");
  await P.until(
    `/运行中|出错|错误/.test(document.querySelector('[data-testid=session-state]').textContent)`,
    { tries: 60, gap: 300, label: '会话进入运行中或出错' },
  );

  const stateAfterStart = await api('/api/state');
  await check('点「开始抓取」后服务端真的起来了（state=running）', () => {
    assert.equal(stateAfterStart.session.state, 'running', JSON.stringify({
      state: stateAfterStart.session.state,
      error: stateAfterStart.session.lastError,
    }));
    assert.equal(stateAfterStart.session.symbol, 'CME_MINI:MNQ1!');
  }, { state: stateAfterStart.session.state, err: stateAfterStart.session.lastError });

  await check('默认不主动操作图表（autoPull=false，避免不必要风控）', () => {
    assert.equal(stateAfterStart.session.autoPull, false);
  }, stateAfterStart.session.autoPull);

  await sleep(1500);
  await shot(cdp, pageSess, '02-running.png');

  // 打开自动回拉
  await P.ev("document.querySelector('[data-testid=autopull]').click()");
  await P.until("document.querySelector('[data-testid=autopull]').classList.contains('switch-on')", {
    tries: 30,
    gap: 200,
    label: '开关变成 on',
  });
  const statePullOn = await api('/api/state');
  await check('切换自动回拉开关后，服务端会话状态同步变了', () => {
    assert.equal(statePullOn.session.autoPull, true);
  }, statePullOn.session.autoPull);

  // 再关掉，确认是双向的（关掉后应回到纯被动监听）
  await P.ev("document.querySelector('[data-testid=autopull]').click()");
  await P.until("!document.querySelector('[data-testid=autopull]').classList.contains('switch-on')", {
    tries: 30,
    gap: 200,
    label: '开关变回 off',
  });
  const statePullOff = await api('/api/state');
  await check('关掉自动回拉后回到被动监听', () => {
    assert.equal(statePullOff.session.autoPull, false);
  }, statePullOff.session.autoPull);

  await P.ev("document.querySelector('[data-testid=stop]').click()");
  await P.until("document.querySelector('[data-testid=session-state]').textContent.includes('空闲')", {
    tries: 80,
    gap: 300,
    label: '会话回到空闲',
  });
  const stateStopped = await api('/api/state');
  await check('点「停止抓取」后会话干净收尾（state=idle）', () => {
    assert.equal(stateStopped.session.state, 'idle');
  }, stateStopped.session.state);

  const logText = await P.ev("document.querySelector('[data-testid=logbox]').textContent");
  await check('日志面板里有"自动回拉"这类真实事件（说明 SSE 推到了页面上）', () => {
    assert.ok(logText.includes('自动回拉'), `日志片段：${logText.slice(0, 300)}`);
  }, logText.slice(0, 200));

  // ---- 7. 覆盖视图 ----
  section('交互：覆盖视图');
  await P.ev("document.querySelector('[data-testid=tab-coverage]').click()");
  await P.until("document.querySelectorAll('[data-testid=calendar] .cal-cell').length > 0", {
    tries: 80,
    gap: 250,
    label: '月历格子出现',
  });
  await sleep(900);

  const covGeo = await P.json(`JSON.stringify((() => {
    const cells = [...document.querySelectorAll('[data-testid=calendar] .cal-cell')];
    // 时间轴整个可能被前端 v-if 掉，所以这里一律走空值保护。
    // 不能假设 canvas 一定在 —— 采集脚本自己抛异常，会把下面所有无关断言一起带崩。
    const cv = document.querySelector('[data-testid=matrix]');
    const wrap = cv ? cv.parentElement : null;
    const r = cv ? cv.getBoundingClientRect() : null;
    const blanks = [...document.querySelectorAll('[data-testid=calendar] > div')].filter(d => !d.classList.contains('cal-cell'));
    return {
      hasCanvas: !!cv,
      hasHint: !!document.querySelector('[data-testid=matrix-hint]'),
      cells: cells.length,
      firstCellW: Math.round(cells[0].getBoundingClientRect().width),
      canvasW: r ? Math.round(r.width) : 0,
      canvasH: r ? Math.round(r.height) : 0,
      canvasAttrW: cv ? cv.width : 0,
      wrapW: wrap ? Math.round(wrap.getBoundingClientRect().width) : 0,
      wrapClientH: wrap ? wrap.clientHeight : 0,
      wrapScrollH: wrap ? wrap.scrollHeight : 0,
      wrapMaxH: wrap ? (parseFloat(getComputedStyle(wrap).maxHeight) || 0) : 0,
      wrapOverflowY: wrap ? getComputedStyle(wrap).overflowY : '',
      gridCols: getComputedStyle(cells[0].parentElement).gridTemplateColumns.split(' ').length,
      summaryCards: document.querySelectorAll('[data-testid=coverage-summary] .card-tight').length,
      blanks: blanks.length,
      legend: document.body.textContent.includes('应有但缺'),
      gapRows: document.querySelectorAll('[data-testid=gaps-table] tbody tr').length,
      breakRows: document.querySelectorAll('[data-testid=breaks-table] tbody tr').length,
    };
  })())`);

  await check('月历是 7 列栅格', () => {
    assert.equal(covGeo.gridCols, 7, `实际 ${covGeo.gridCols} 列`);
  }, covGeo.gridCols);

  await check('月历里有真实的日期格子（不是空壳）', () => {
    assert.ok(covGeo.cells > 0, `格子数 ${covGeo.cells}`);
    assert.ok(covGeo.firstCellW > 60, `格子宽 ${covGeo.firstCellW}`);
  }, covGeo);

  // 时间轴这一组断言只在前端真的显示它时才跑；关掉时改为断言"它确实不在"，
  // 而不是把断言删掉 —— 删掉之后 e2e 全绿但覆盖面少一大块，还没人记得回来补。
  if (!TIMELINE_ENABLED) {
    await check('时间轴已隐藏：canvas、直方图说明与图例都不在 DOM 里', () => {
      assert.equal(covGeo.hasCanvas, false, 'canvas 还在 —— showTimeline 没生效，或者前端又被改回来了');
      assert.equal(covGeo.hasHint, false, '直方图说明还在，说明藏的是别的块');
      assert.equal(covGeo.legend, false, '图例还在，同上');
    }, covGeo);
  } else {
    await check('时间轴 canvas 有非零尺寸，且宽度不越过容器（超了就靠外层滚动）', () => {
      assert.ok(covGeo.canvasW > 300, `canvas 宽 ${covGeo.canvasW}`);
      assert.ok(covGeo.canvasH > 100, `canvas 高 ${covGeo.canvasH}`);
      assert.ok(covGeo.canvasAttrW >= covGeo.canvasW, `位图宽 ${covGeo.canvasAttrW} 应 ≥ CSS 宽 ${covGeo.canvasW}`);
    }, covGeo);

    await check('时间轴容器高度被 max-height 卡住（天数多时内部滚动，不撑破页面）', () => {
      assert.ok(covGeo.wrapMaxH > 0, `wrap 没有 max-height：${covGeo.wrapMaxH}`);
      assert.ok(
        covGeo.wrapClientH <= covGeo.wrapMaxH + 1,
        `clientHeight=${covGeo.wrapClientH} 超过了 max-height=${covGeo.wrapMaxH}`,
      );
      assert.notEqual(covGeo.wrapOverflowY, 'visible', '容器必须能滚动，否则天数一多就把页面撑破');
      // canvas 是真内容，必须完整地装在滚动容器里，而不是被容器裁掉一半却滚不到
      assert.ok(
        covGeo.wrapScrollH + 1 >= covGeo.canvasH,
        `scrollHeight=${covGeo.wrapScrollH} < canvasHeight=${covGeo.canvasH}，底部会被裁掉且滚不到`,
      );
    }, covGeo);

    await check('图例说明了"应有但缺"（否则红格子没法解释）', () => {
      assert.equal(covGeo.legend, true);
    }, covGeo.legend);
  }

  await check('汇总卡片是 5 张（覆盖天数/覆盖率/缺口/断档/下一个目标）', () => {
    assert.equal(covGeo.summaryCards, 5, `实际 ${covGeo.summaryCards}`);
  }, covGeo.summaryCards);

  await check('缺口表与断档表都渲染出了行', () => {
    assert.ok(covGeo.gapRows > 0, `缺口表 ${covGeo.gapRows} 行`);
    assert.ok(covGeo.breakRows > 0, `断档表 ${covGeo.breakRows} 行`);
  }, { gaps: covGeo.gapRows, breaks: covGeo.breakRows });

  // 直方图说明也挂在时间轴上，跟同一个开关走。
  // 这里不能无脑 querySelector(...).textContent：元素不在时页面内求值会直接抛，
  // 整轮 e2e 就此中断 —— 那不是"一条失败"，是后面全都不跑了。
  if (TIMELINE_ENABLED) {
    const histLine = await P.ev("document.querySelector('[data-testid=matrix-hint]').textContent");
    await check('时间轴带直方图说明', () => {
      assert.ok(histLine.includes('直方图'), histLine);
    }, histLine);
  }

  // 逐格对账：把 canvas 上画出来的颜色和接口给的位图逐格比。
  // 光看截图只能看出"像是有点少"，看不出到底少在哪 —— 这个断言能。
  // 时间轴关着的时候这整块没有意义（canvas 都不存在），但不删 ——
  // 恢复时间轴时这一块就是现成的回归网，删了没人会想起来重建。
  if (TIMELINE_ENABLED) {
    const pix = await P.json(`(async () => {
      const slug = document.querySelector('[data-testid=symbol-select]').value;
      const cal = await fetch('/api/symbols/' + encodeURIComponent(slug) + '/calendar').then((r) => r.json());
      const cv = document.querySelector('[data-testid=matrix]');
      const L = window.__tv && window.__tv.layout();
      if (!L) return { error: '拿不到布局（window.__tv.layout() 为空）' };
      const dpr = cv.width / L.width;
      const W = cv.width;
      const H = cv.height;
      const img = cv.getContext('2d').getImageData(0, 0, W, H).data;
      const at = (x, y) => {
        const px = Math.round(x * dpr);
        const py = Math.round(y * dpr);
        if (px < 0 || py < 0 || px >= W || py >= H) return null;
        const i = (py * W + px) * 4;
        return [img[i], img[i + 1], img[i + 2], img[i + 3]];
      };
      // 分类只看"偏红 / 偏蓝 / 灰"三个方向 —— 抗锯齿和 color-mix 的精确值不该由测试来锁
      const cls = (p) => {
        if (!p || p[3] < 200) return 'blank';
        const [r, g, b] = p;
        if (r > b + 18 && r > g + 8) return '2';
        if (b > r + 18) return '1';
        return '0';
      };
      const counts = { 0: 0, 1: 0, 2: 0, blank: 0 };
      const expect = { 0: 0, 1: 0, 2: 0 };
      const mismatch = [];
      for (let r = 0; r < cal.days.length; r++) {
        const y = L.matrixTop + r * L.rowH + L.rowH / 2;
        for (let i = 0; i < cal.slotsPerDay; i++) {
          const x = L.gutter + i * L.bucketW + L.bucketW / 2;
          const got = cls(at(x, y));
          const st = cal.days[r].states[i];
          counts[got] = (counts[got] || 0) + 1;
          // '3' 是淡蓝，会被归进 '1'，不参与对账
          if (st !== '3') expect[st]++;
          if (st !== '3' && got !== st && mismatch.length < 10) mismatch.push({ r, i, st, got });
        }
      }
      return { counts, expect, mismatch, bucketW: L.bucketW, dpr, days: cal.days.length, slots: cal.slotsPerDay };
    })()`);

    await check('每格画出来的颜色与位图状态一一对应（不是靠肉眼看着差不多）', () => {
      assert.ok(!pix.error, pix.error);
      assert.equal(
        pix.mismatch.length,
        0,
        `对不上 ${pix.mismatch.length} 格，例如 ${JSON.stringify(pix.mismatch.slice(0, 4))}`,
      );
    }, pix);

    await check('canvas 上"有数据"的格子数 = 位图里 \'1\' 的个数（差一个都算漏画）', () => {
      // 这条是给"主题变量读不到 → fillStyle 被静默忽略 → 整层图形不画"那个坑兜底的：
      // 当时位图里 2274 个 '1'，画布上一个都没画出来，而其它断言全都绿。
      assert.equal(pix.counts['1'], pix.expect['1'], `画了 ${pix.counts['1']}，应画 ${pix.expect['1']}`);
      assert.ok(pix.expect['1'] > 100, `位图里只有 ${pix.expect['1']} 个"有数据"格子，样本太少不足以说明问题`);
      assert.equal(pix.counts['blank'], 0, `有 ${pix.counts['blank']} 格是透明的（该画没画）`);
    }, { drawn: pix.counts, expect: pix.expect });
  }

  await shot(cdp, pageSess, '03-coverage.png');
  // 整页截图，方便肉眼过一遍
  await shot(cdp, pageSess, '04-coverage-full.png', { full: true });

  // 点一天，验证单日明细。
  // 挑一个"在范围内"的日子 —— 月历把整月都画出来了，最前面几格可能是范围外的。
  await P.ev(`(() => {
    const cells = [...document.querySelectorAll('[data-testid=calendar] .cal-cell')];
    const c = cells.find((x) => !x.textContent.includes('不在范围')) || cells[0];
    c.click();
  })()`);
  await P.until("!!document.querySelector('[data-testid=calendar] .cal-cell-sel')", { tries: 40, gap: 250, label: '选中态出现' });
  await sleep(500);
  const dayDetail = await P.ev("document.querySelector('[data-testid=day-detail]').textContent");
  await check('点某一天后，单日明细面板真的换了内容（不是占位文案）', () => {
    assert.ok(/根数/.test(dayDetail), dayDetail.slice(0, 200));
    assert.ok(
      /有数据的连续时段/.test(dayDetail) || /整日空洞/.test(dayDetail),
      dayDetail.slice(0, 200),
    );
    // 这条是回归：一度出现过「标题说选中了某天、正文还是"点左边日历里任意一天"」的自相矛盾
    assert.ok(!/点左边日历里的任意一天/.test(dayDetail), '选中后占位文案必须消失');
  }, dayDetail.slice(0, 240));

  // 反过来：点一个明确在数据范围之外的日子，也必须给答复（而不是留个空占位）
  await P.ev(`(() => {
    const cells = [...document.querySelectorAll('[data-testid=calendar] .cal-cell')];
    const c = cells.find((x) => x.textContent.includes('不在范围')) || cells[cells.length - 1];
    c.click();
  })()`);
  await sleep(600);
  const outsideDetail = await P.ev("document.querySelector('[data-testid=day-detail]').textContent");
  await check('点范围外的日子会说清"这天不在范围内"，而不是装死', () => {
    assert.ok(!/点左边日历里的任意一天/.test(outsideDetail), '占位文案必须消失');
    assert.ok(/不在范围/.test(outsideDetail), outsideDetail.slice(0, 240));
  }, outsideDetail.slice(0, 240));

  // ---- 8. 数据管理 ----
  section('交互：数据管理');
  await P.ev("document.querySelector('[data-testid=tab-data]').click()");
  await P.until("document.querySelectorAll('[data-testid=symbols-table] tbody tr').length > 0", {
    tries: 60,
    gap: 250,
    label: '品种表出现',
  });
  const dataGeo = await P.json(`JSON.stringify((() => {
    const rows = [...document.querySelectorAll('[data-testid=symbols-table] tbody tr')];
    const first = rows[0] ? rows[0].textContent : '';
    return {
      rows: rows.length,
      firstRow: first.slice(0, 160),
      rawRows: document.querySelectorAll('[data-testid=raw-table] tbody tr').length,
      hasExport: document.body.textContent.includes('导出 CSV'),
      hasLayout: document.body.textContent.includes('data/db'),
    };
  })())`);
  await check('品种表列出了库文件与根数', () => {
    assert.ok(dataGeo.rows >= 1, `${dataGeo.rows} 行`);
    assert.ok(/MNQ/.test(dataGeo.firstRow), dataGeo.firstRow);
  }, dataGeo);
  await check('数据管理页说明了"一个品种一个 sqlite"的存储布局', () => {
    assert.equal(dataGeo.hasLayout, true);
  }, dataGeo);
  await check('原始抓包表有行（并标出待入库/已入库）', () => {
    assert.ok(dataGeo.rawRows > 0, `raw 表 ${dataGeo.rawRows} 行`);
  }, dataGeo.rawRows);
  await shot(cdp, pageSess, '05-data.png');

  // ---- 9. 深浅色都过一遍 ----
  section('深浅色切换');
  await P.ev("document.querySelector('[data-testid=theme]').click()");
  await sleep(600);
  const darkGeo = await P.json(`JSON.stringify(({
    isDark: document.documentElement.classList.contains('dark'),
    bodyBg: getComputedStyle(document.body).backgroundColor,
    ink: getComputedStyle(document.body).color,
  }))`);
  await check('切到深色后 html.dark 生效，且文字色是浅的', () => {
    assert.equal(darkGeo.isDark, true);
    assert.notEqual(darkGeo.bodyBg, 'rgb(255, 255, 255)');
    // #e9e6de -> rgb(233, 230, 222)
    const m = /rgb\((\d+), (\d+), (\d+)\)/.exec(darkGeo.ink);
    assert.ok(m && Number(m[1]) > 150, `深色下正文色偏暗：${darkGeo.ink}`);
  }, darkGeo);
  await P.ev("document.querySelector('[data-testid=tab-coverage]').click()");
  await sleep(800);
  await shot(cdp, pageSess, '06-dark-coverage.png');

  // 深色下 canvas 也得重画（否则还是浅色主题的配色）。同样只在前端显示时间轴时才验。
  if (TIMELINE_ENABLED) {
    const canvasDark = await P.json(`JSON.stringify((() => {
      const cv = document.querySelector('[data-testid=matrix]');
      const ctx = cv.getContext('2d');
      const d = ctx.getImageData(0, 0, Math.min(cv.width, 400), Math.min(cv.height, 200)).data;
      let nonEmpty = 0;
      let light = 0;
      for (let i = 0; i < d.length; i += 4) {
        if (d[i + 3] > 0) { nonEmpty++; if (d[i] + d[i + 1] + d[i + 2] > 300) light++; }
      }
      return { nonEmpty, light, total: d.length / 4 };
    })())`);
    await check('深色模式下时间轴 canvas 仍然画出了内容', () => {
      assert.ok(canvasDark.nonEmpty > 500, `非透明像素 ${canvasDark.nonEmpty}`);
    }, canvasDark);
  }

  await shot(cdp, pageSess, '07-dark-data.png', { full: true });

  await check('整轮跑完依然没有 JS 异常', () => {
    assert.equal(pageErrors.length, 0, pageErrors.join(' | '));
  }, pageErrors);
} catch (e) {
  console.error(`\n[e2e 中断] ${e.message}`);
  if (process.env.TV_TRACE) console.error(e.stack);
  process.exitCode = 1;
} finally {
  const c0 = Date.now();
  await cleanup();
  console.log(`\n[收尾] 关浏览器 + 清临时 profile：${Date.now() - c0}ms`);
}

console.log(`\n${passed} 项通过${failed ? `，${failed} 项失败` : '，全部通过'}`);
console.log(`总耗时：${((Date.now() - t0) / 1000).toFixed(1)}s`);
console.log(`截图目录：${SHOT_DIR}`);

// ----------------------------------------------------------------

// 截图只是给人看的产物，断言不依赖它 —— 所以它绝不该有能力拖死整轮测试。
// 实测：深色模式下的整页截图（captureBeyondViewport）会长时间不返回，
// 而这个 Promise 一旦 pending，后面的断言就再也不会跑，进程干挂在那儿。
// 于是这里加超时：截不出来就报一声继续，别把 e2e 变成"跑了十分钟什么都没有"。
// 超时后那个 CDP 请求还挂在 pending 表里，无所谓 —— 会话结束就一起没了。
async function shot(cdpClient, sessionId, name, { full = false, timeoutMs = 15000 } = {}) {
  try {
    const r = await Promise.race([
      cdpClient.send(
        'Page.captureScreenshot',
        { format: 'png', captureBeyondViewport: full },
        sessionId,
      ),
      new Promise((_, rej) =>
        setTimeout(() => rej(new Error(`超过 ${timeoutMs}ms 没返回`)), timeoutMs).unref?.(),
      ),
    ]);
    fs.writeFileSync(path.join(SHOT_DIR, name), Buffer.from(r.data, 'base64'));
  } catch (e) {
    console.log(`  (截图 ${name} 失败：${e.message})`);
  }
}
