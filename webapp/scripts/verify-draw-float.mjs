// 一次性验证脚本：绘图工具浮窗是否渲染、能否拖动、按钮是否真的切换 store.tool。
// 跑法：node verify-draw-float.mjs <accountId>
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const ACCOUNT = process.argv[2];
const PAGE = `http://127.0.0.1:5174/trade/${ACCOUNT}`;
const PORT = 9333;
const OUT = path.join(os.tmpdir(), 'draw-float.png');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'cdp-'));
const edge = spawn(EDGE, [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  `--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`,
  '--window-size=1500,900', PAGE,
], { stdio: 'ignore' });

let target;
for (let i = 0; i < 60; i++) {
  try {
    const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
    target = list.find((t) => t.type === 'page' && t.url.startsWith('http://127.0.0.1:5174'));
    if (target?.webSocketDebuggerUrl) break;
  } catch { /* 还没起来 */ }
  await sleep(300);
}
if (!target) { console.error('FAIL: 拿不到 CDP target'); edge.kill(); process.exit(1); }

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r) => { ws.onopen = r; });
let id = 0;
const waits = new Map();
const errors = [];
ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id && waits.has(m.id)) { waits.get(m.id)(m); waits.delete(m.id); return; }
  if (m.method === 'Runtime.exceptionThrown') errors.push(JSON.stringify(m.params.exceptionDetails.text));
  if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error') errors.push(m.params.entry.text);
  if (m.method === 'Network.responseReceived' && m.params.response.status >= 400) errors.push(m.params.response.status + ' ' + m.params.response.url);
};
const send = (method, params) => new Promise((res, rej) => {
  const i = ++id;
  waits.set(i, (m) => (m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result)));
  ws.send(JSON.stringify({ id: i, method, params: params || {} }));
});
const ev = async (expr) => (await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })).result.value;
const until = async (expr, ms = 20000) => {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) { if (await ev(expr)) return true; await sleep(200); }
  return false;
};
let bad = 0;
const check = (name, ok, actual) => { console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${actual !== undefined ? ' → ' + actual : ''}`); if (!ok) bad++; };

await send('Runtime.enable'); await send('Page.enable'); await send('Log.enable'); await send('Network.enable');

// 浮窗：带拖动把手的那个 div
const SEL = `[...document.querySelectorAll('div')].find(d => d.querySelector(':scope > span[title="按住拖动这个工具浮窗"]'))`;
const ready = await until(`!!(${SEL})`, 30000);
check('浮窗渲染出来（5 个绘图按钮 + 把手）', ready);
if (!ready) { console.log('页面文本片段：', (await ev('document.body.innerText')).slice(0, 300)); }

if (ready) {
  const before = await ev(`(() => { const b = ${SEL}; const r = b.getBoundingClientRect();
    return JSON.stringify({ x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), buttons: [...b.querySelectorAll('button')].map(x => x.textContent.trim()) }); })()`);
  console.log('  初始位置/按钮：', before);
  const g = JSON.parse(before);
  check('按钮是五种工具', g.buttons.length === 5 && g.buttons.includes('趋势线'), g.buttons.join('/'));

  // 拖动把手 +200,-120（用 CDP 真实鼠标事件，走指针捕获那条路）
  const grip = JSON.parse(await ev(`(() => { const r = ${SEL}.querySelector('span[title="按住拖动这个工具浮窗"]').getBoundingClientRect();
    return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`));
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: grip.x, y: grip.y, button: 'left', clickCount: 1, buttons: 1 });
  await sleep(80);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: grip.x + 200, y: grip.y - 120, button: 'left', buttons: 1 });
  await sleep(80);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: grip.x + 200, y: grip.y - 120, button: 'left', buttons: 0 });
  await sleep(200);
  const after = JSON.parse(await ev(`(() => { const r = ${SEL}.getBoundingClientRect();
    return JSON.stringify({ x: Math.round(r.left), y: Math.round(r.top) }); })()`));
  console.log('  拖后位置：', JSON.stringify(after));
  check('拖动能真的移动浮窗（Δx≈200、Δy≈-120）', Math.abs(after.x - g.x - 200) <= 6 && Math.abs(after.y - g.y + 120) <= 6, `${after.x - g.x},${after.y - g.y}`);

  // 点「矩形」→ 按钮进激活态；画布进入该工具（编辑模板条出现）
  const clicked = await ev(`(() => { const b = [...${SEL}.querySelectorAll('button')].find(x => x.textContent.trim() === '矩形');
    b.click(); return b.className.includes('bg-accent-soft'); })()`);
  await sleep(300);
  const active = await ev(`[...${SEL}.querySelectorAll('button')].filter(b => b.className.includes('bg-accent-soft')).map(b => b.textContent.trim()).join(',')`);
  check('点按钮后进入绘图态', active === '矩形', active);

  await send('Page.captureScreenshot', { format: 'png' }).then((r) => fs.writeFileSync(OUT, Buffer.from(r.data, 'base64')));
  console.log('截图：' + OUT);
}

check('没有页面异常', errors.length === 0, errors.slice(0, 3).join(' | '));
ws.close(); edge.kill();
console.log(bad === 0 ? 'RESULT OK' : `RESULT FAIL(${bad})`);
process.exit(bad === 0 ? 0 : 1);
