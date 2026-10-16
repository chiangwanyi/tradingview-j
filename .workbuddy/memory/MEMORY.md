# tradingview-j 项目长期笔记

## 这个项目是什么

通过 CDP 接入**用户本人已登录的 Chrome**，旁路抓 TradingView 图表页的 WebSocket 行情帧，
解码成 OHLCV 持续入库，目标是**把一条时间轴尽量填满**（学习/研究用途，非实时）。

零第三方依赖：Node 22 自带 `WebSocket` 与 `node:sqlite`。Node 路径：
`C:/Users/jiang/.workbuddy/binaries/node/versions/22.22.2-6/node.exe`

## 项目约定

- **命令入口只有一个**：`bin/tv.mjs`。新增能力就加子命令，别散落脚本。
- **协议层不硬编码方法名**：按形状找 `{ s: [ { i, v: [数值…] } ] }`。协议改版时不容易碎。
- **入库幂等**：主键 `(symbol, series, t)` + `ON CONFLICT DO UPDATE`。
  这让"反复来回拖、窗口大面积重叠"变成无害操作。
- **自测必须离线可跑**：`node test/selftest.mjs`，不依赖 Chrome。
  **不要把自己对协议字段的猜测写成自测当事实** —— 会制造虚假的绿色（踩过）。
- **注释写"为什么"和踩过的坑**，不写"做了什么"。

## 关键事实（别忘）

- Chrome ≥ 136：对默认用户数据目录加 `--remote-debugging-port` 被**静默忽略**。
  只能走 `chrome://inspect/#remote-debugging` 勾选开关（Chrome ≥ 144），每个浏览器会话要重新勾。
- 新版 Chrome 只开放**浏览器级 WS**，`/json/version` 与 `/json/list` 一律 404。
  页面发现走 `Target.getTargets` + `Target.attachToTarget`。
- Chrome 不补发 `Network.enable` 之前已建立的 WS 的 `webSocketCreated` → 帧 `url` 为空。
- **series id（`sds_1`/`sds_2`…）是每条连接的临时订阅号，重连会换号**。别当身份用。
- 图表**按需**取数：可视范围走出缓存窗口才发 `request_more_data`。
  这是"填时间轴"的立足点 —— 拨动图表即可，不必手动回放。
- 周期只能靠相邻时间戳众数差反推。`lbs.bar_close_time` 是图表右侧可视边界，**不是**每根 K 线的收盘时间。
- 期货休市时间（CME，UTC）：每日 21:00–22:00 结算，周六全天 + 周日 22:00 前。

## 用户偏好

- 需要"先纠正认知误区，再给可执行结论"，并结合他自己工程的具体文件举例。
- 喜欢可视化图示配合讲解。
- 重视"结论要有实测支撑"，讨厌凭空推断 —— 汇报时要如实标出哪些是验证过的、哪些还是猜测。
