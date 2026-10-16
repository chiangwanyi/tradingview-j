# tradingview-j 项目长期笔记

## 这个项目是什么

通过 CDP 接入**用户本人已登录的 Chrome**，旁路抓 TradingView 图表页的 WebSocket 行情帧，
解码成 OHLCV 持续入库，目标是**把一条时间轴尽量填满**（学习/研究用途，非实时）。

形态是**本地 Node 项目 + 网页工作台**：`npm start` 起 `server/index.mjs`（默认 `127.0.0.1:8787`），
页面三页签（抓取控制 / 覆盖视图 / 数据管理），CLI `bin/tv.mjs` 保留全部命令。

零第三方依赖：Node 22 自带 `WebSocket` 与 `node:sqlite`。前端是 Vue 3 + Tailwind 4 的
**零构建**组合（两个库放 `web/vendor/`）。Node 路径：
`C:/Users/jiang/.workbuddy/binaries/node/versions/22.22.2-6/node.exe`

## 项目约定

- **命令入口两个**：网页 `server/index.mjs`（用户日常用）+ `bin/tv.mjs`（脚本/排障）。
  两边必须共用同一个控制器 `src/session.mjs::CaptureSession`，别各写一套。
- **存储：一个品种一个 `.sqlite`**，落在 `data/db/<slug>.sqlite`，`data/db/index.json` 存映射。
  改名/迁移走 `src/registry.mjs` + `src/migrate.mjs`，**旧库永远只读、不删**。
- **协议层不硬编码方法名**：按形状找 `{ s: [ { i, v: [数值…] } ] }`。协议改版时不容易碎。
- **入库幂等**：主键 `(symbol, series, t)` + `ON CONFLICT DO UPDATE`。
  这让"反复来回拖、窗口大面积重叠"变成无害操作。
- **算根数先去重**：series 换号会留重复行。对外报"根数"一律 `COUNT(DISTINCT t)`，
  物理行数单独作 `rows` 暴露 —— 两者不等本身就是"发生过重连"的信号。
- **读-改-写的接口不能在循环外缓存快照**（`index.json` 踩过，迁移后索引被旧快照抹空）。
- **自测必须离线可跑**，且分三层：
  `test/selftest.mjs`（后端纯 Node）/ `test/ui-selftest.mjs`（前端纯函数）/ `test/e2e.mjs`（真浏览器）。
  加断言时优先加在 e2e —— jsdom 读不到 Tailwind 注入的样式，布局类断言只有真浏览器答得了。
- **`check()` 必须 async 且真 await**，否则 async 断言回调的 Promise 被丢掉 → 假绿。
- **不要把自己对协议字段的猜测写成自测当事实** —— 会制造虚假的绿色（踩过）。
- **注释写"为什么"和踩过的坑**，不写"做了什么"。

## 关键事实（别忘）

- Chrome ≥ 136：对默认用户数据目录加 `--remote-debugging-port` 被**静默忽略**。
  只能走 `chrome://inspect/#remote-debugging` 勾选开关（Chrome ≥ 144），每个浏览器会话要重新勾。
- 新版 Chrome 只开放**浏览器级 WS**，`/json/version` 与 `/json/list` 一律 404。
  页面发现走 `Target.getTargets` + `Target.attachToTarget`。
  **`Target.createTarget` 不接受 `width`/`height`**（Chrome 155 报
  "Target position can only be set for new windows"），视口用 `Emulation.setDeviceMetricsOverride`。
- Chrome 不补发 `Network.enable` 之前已建立的 WS 的 `webSocketCreated` → 帧 `url` 为空。
- **series id（`sds_1`/`sds_2`…）是每条连接的临时订阅号，重连会换号**。别当身份用。
- 图表**按需**取数：可视范围走出缓存窗口才发 `request_more_data`。
  这是"填时间轴"的立足点 —— 拨动图表即可，不必手动回放。
- 周期只能靠相邻时间戳众数差反推。`lbs.bar_close_time` 是图表右侧可视边界，**不是**每根 K 线的收盘时间。
- 期货休市时间（CME，UTC）：每日 21:00–22:00 结算，周六全天 + 周日 22:00 前。
- **风控**：自动回拉默认关闭，是刻意的。主动操作图表会产生非人类交互序列。

## 前端零构建的坑

- Tailwind 4 里**未分层的普通 CSS 永远赢过分层 CSS** → `web/style.css` 只写
  `.card`/`.btn`/`.switch` 这类结构性样式，绝不写 margin/width（写了就盖掉工具类）。
- Tailwind 的 preflight **不设 `font-size`** → 必须显式写 `body { font-size: 14px }`。
- **Tailwind 4 会 tree-shake 主题变量**：没有任何工具类引用的变量不会输出到 `:root`。
  canvas 用 `getComputedStyle` 读它会拿到空串，而 `ctx.fillStyle = ''` 是静默忽略 →
  整层图形无声消失。**`@theme static` 不能省**，且 canvas 取色要逐项兜底 + 缺变量时报 error。
  判定手段：e2e 里逐格采样 canvas 像素与接口位图对账，别靠看截图。
- **canvas 里别用 CSS 的 `color-mix()`**（`fillStyle` 解析支持度不保证），用 `globalAlpha`。
- 深色模式用 `html.dark`（0,1,1）压过 `:root`（0,1,0）覆盖变量，不靠顺序赌。
- 时间轴画在 canvas 上，DOM 里拿不到 gutter/rowH → 布局常量经 `window.__tv.layout()` 暴露给 e2e。
- 只要改了 `web/` 下的东西，**必须跑 `node test/e2e.mjs`** —— 纯函数测试看不见布局与颜色。

## 用户偏好

- 需要"先纠正认知误区，再给可执行结论"，并结合他自己工程的具体文件举例。
- 喜欢可视化图示配合讲解。
- 重视"结论要有实测支撑"，讨厌凭空推断 —— 汇报时要如实标出哪些是验证过的、哪些还是猜测。
