// 一次性验证脚本：工具浮窗（位置存档 / 模板条下挂 / 下一根）、收起态交易面板、nATR 存档。
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
  // favicon.ico 的 404 是开发服务器本来就有的，跟这次改动无关
  if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error' && !m.params.entry.text.includes('404')) errors.push(m.params.entry.text);
  if (m.method === 'Network.responseReceived' && m.params.response.status >= 500) errors.push(m.params.response.status + ' ' + m.params.response.url);
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

const SEL = `[...document.querySelectorAll('div')].find(d => d.querySelector(':scope > span[title^="按住拖动"]'))`;
const BOX = `(() => { const r = ${SEL}.getBoundingClientRect(); return JSON.stringify({ x: Math.round(r.left), y: Math.round(r.top), buttons: [...${SEL}.querySelectorAll('button')].map(b => b.textContent.trim()) }); })()`;

await ev(`localStorage.removeItem('tv-draw-toolbar-pos-v1')`);
await send('Page.reload', {});
const ready = await until(`!!(${SEL})`, 30000);
check('浮窗渲染出来', ready);
if (!ready) console.log('页面片段：', (await ev('document.body.innerText')).slice(0, 300));

let first;
if (ready) {
  first = JSON.parse(await ev(BOX));
  console.log('  浮窗：', JSON.stringify(first));
  check('浮窗里有 5 个绘图工具 + 下一根', first.buttons.length === 6 && first.buttons.includes('下一根 ›'), first.buttons.join('/'));
  check('绘图工具与【下一根】之间有分隔线', await ev(`!!${SEL}.querySelector('div.h-4.w-px.bg-line')`));

  check('模拟交易面板默认收起', await ev(`[...document.querySelectorAll('button')].some(b => b.textContent.trim() === '展开面板 ▲')`));

  check('模板条默认不显示', (await ev(`${SEL}.innerText.includes('模板')`)) === false);
  await ev(`[...${SEL}.querySelectorAll('button')].find(b => b.textContent.trim() === '矩形').click()`);
  await sleep(300);
  const panel = await ev(`(() => {
    const wrap = ${SEL};
    const p = [...wrap.querySelectorAll('div')].find(d => d.textContent.trim().startsWith('模板'));
    if (!p) return 'none';
    const pr = p.getBoundingClientRect(); const wr = wrap.getBoundingClientRect();
    return JSON.stringify({ below: pr.top >= wr.bottom - 2, inside: pr.right <= innerWidth + 1, w: Math.round(pr.width) });
  })()`);
  check('点工具后模板条出现并贴在浮窗下面', panel !== 'none' && JSON.parse(panel).below, panel);
  await send('Page.captureScreenshot', { format: 'png' })
    .then((r) => fs.writeFileSync(OUT.replace('.png', '-default.png'), Buffer.from(r.data, 'base64')));

  const grip = JSON.parse(await ev(`(() => { const r = ${SEL}.querySelector('span[title^="按住拖动"]').getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`));
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: grip.x, y: grip.y, button: 'left', clickCount: 1, buttons: 1 });
  await sleep(80);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: grip.x + 220, y: grip.y + 140, button: 'left', buttons: 1 });
  await sleep(80);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: grip.x + 220, y: grip.y + 140, button: 'left', buttons: 0 });
  await sleep(300);
  const dragged = JSON.parse(await ev(BOX));
  console.log('  拖后：', JSON.stringify({ x: dragged.x, y: dragged.y }));
  check('拖动能移动浮窗', Math.abs(dragged.x - first.x - 220) <= 6 && Math.abs(dragged.y - first.y - 140) <= 6, `${dragged.x - first.x},${dragged.y - first.y}`);

  const setNatr = async (title, value) => ev(`(() => { const el = document.querySelector('input[title^="${title}"]'); if (!el) return 'no-input';
    el.value = '${value}'; el.dispatchEvent(new Event('change', { bubbles: true })); return el.value; })()`);
  const nSet = await setNatr('止损距离', '2.5');
  const lossSet = await setNatr('这笔交易最多亏', '850');
  check('nATR 两格能输入', nSet === '2.5' && lossSet === '850', `${nSet} / ${lossSet}`);
  console.log('  存档：', await ev(`JSON.stringify({ pos: localStorage.getItem('tv-draw-toolbar-pos-v1'), natr: localStorage.getItem('tv-trade-natr-v1') })`));

  await send('Page.reload', {});
  await until(`!!(${SEL})`, 30000);
  await until(`document.querySelector('input[title^="止损距离"]')`, 20000);
  await sleep(500);
  const after = JSON.parse(await ev(BOX));
  check('刷新后浮窗回到原位', Math.abs(after.x - dragged.x) <= 2 && Math.abs(after.y - dragged.y) <= 2, `${after.x},${after.y}`);
  const nAfter = await ev(`document.querySelector('input[title^="止损距离"]').value`);
  const lossAfter = await ev(`document.querySelector('input[title^="这笔交易最多亏"]').value`);
  check('刷新后 nATR 两格还在', nAfter === '2.5' && lossAfter === '850', `${nAfter} / ${lossAfter}`);
  check('刷新后模板条又收起来了', (await ev(`${SEL}.innerText.includes('模板')`)) === false);

  await send('Page.captureScreenshot', { format: 'png' }).then((r) => fs.writeFileSync(OUT, Buffer.from(r.data, 'base64')));
  console.log('截图：' + OUT);
}

check('没有页面异常（5xx / 未捕获）', errors.length === 0, errors.slice(0, 3).join(' | '));
ws.close(); edge.kill();
console.log(bad === 0 ? 'RESULT OK' : `RESULT FAIL(${bad})`);
process.exit(bad === 0 ? 0 : 1);
