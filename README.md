# tradingview-j

通过 CDP 接入**你本人已登录的 Chrome**，旁路抓取 TradingView 图表页的 WebSocket 行情帧，解码成
OHLCV 并持续增量入库。零第三方依赖（Node 22 自带 `WebSocket` 与 `node:sqlite`）。

目标连接：

```
wss://prodata.tradingview.com/socket.io/websocket?from=chart/EuR5QG29/&date=...&type=chart&auth=sessionid
```

`auth=sessionid` 表示鉴权走 Cookie —— 所以必须借你浏览器的会话，这也决定了接入方式的选择。

## 为什么是这条路

| 方案 | 结论 |
|---|---|
| **CDP 接入已登录 Chrome** | **选它**。复用会话、无需重新登录、能看到二进制帧、还能主动发帧要长历史 |
| Chrome 扩展猴补 WebSocket | 不用重启，但从页面外部难以主动发帧 |
| mitmproxy 中间人 | 要装根证书，且与浏览器实现耦合少但配置重 |

一个关键事实（Chrome 155 实测确认）：**Chrome >= 136 起，给默认用户数据目录加
`--remote-debugging-port` 会被静默忽略**，`/json/version` 直接连接被拒。所以"关掉 Chrome
再加参数重启"这条路已经走不通。官方给出的替代是 Chrome >= 144 的开关：

```
chrome://inspect/#remote-debugging
  -> 勾选 "Allow remote debugging for this browser instance"
```

这条路径能在**当前运行中的实例**上生效，不需要重启、不需要重新登录。代价是每次外部发起连接，
Chrome 会弹一次授权框要求点 Allow；且每个新的浏览器会话都要重新勾一次。

## 跑起来

```bash
# 0. 诊断（会明确告诉你卡在哪一步）
node bin/tv.mjs doctor

# 1. 先去 Chrome 里打开 chrome://inspect/#remote-debugging 勾选开关
#    然后确认能看到 TradingView 图表页
node bin/tv.mjs targets

# 2. 抓帧。--reload 会刷新页面以重新建立 WS 连接，
#    这样才能抓到初始的整段历史（timescale_update）
node bin/tv.mjs capture --reload --hook-ws --sample 40 --duration 120

# 3. 解码入库
node bin/tv.mjs ingest

# 4. 看结果 / 导出
node bin/tv.mjs stats
node bin/tv.mjs export --out data/bars.csv
```

## 持续跟盘

`capture` 不设 `--duration` 就一直跑；另开一个终端循环入库即可：

```bash
node bin/tv.mjs capture --duration 3600     # 终端 A
while true; do node bin/tv.mjs ingest; sleep 60; done   # 终端 B
```

`ingest` 按 `(symbol, series, t)` 幂等，重复入库不会产生重复行，正在走的那根 K 线被
`du` 刷新时是覆盖旧值。注意 SQLite 单写者模型：别让两个 `ingest` 同时跑。

`capture` 中途断了（Ctrl+C 或 Chrome 关掉）需要重新跑；TradingView 的 WebSocket 断开后
客户端会自动重连，但重连会新建连接 —— 那时若 `Network.enable` 已生效，
`webSocketCreated` 就会正常上报，`url` 也不再为空。

## 主动要更多历史

`capture --hook-ws` 会在页面里装一个 WebSocket 记录器（刷新后生效），于是可以自己发帧：

```bash
node bin/tv.mjs eval --expr '__tvSockets()'
node bin/tv.mjs send --frame '{"m":"request_more_data","p":["cs_xxxx","sds_1",300]}'
```

`send` 会自动补上 `~m~<长度>~m~` 分帧。具体方法名与参数需要按抓到的真实请求照着模仿 ——
先 `capture` 抓一份，用 `sample` 文件看清它自己怎么发的，再照着发。

## 帧格式

外层是自定义分帧，不是裸 JSON：

```
~m~<负载字符长度>~m~{"m":"du","p":[...]}    消息
~h~<n>                                     心跳，必须原样回送，否则连接会被服务端断开
```

三个坑，代码里都已处理：

1. **心跳会直接贴在 JSON 负载后面**（心跳没有 `~m~` 头）。按帧头切分会把
   `{"m":"heartbeat","p":[]}~h~7` 当成一段导致 JSON 解析失败，所以 `splitFrame` 是正向扫描的 tokenizer。
2. **声明长度的语义**（字符还是字节）不保证一致。长度对不上时退化为"切到下一个帧头之前"，宁可多吞也不丢数据。
3. **取 K 线不硬编码方法名**。任何形状为 `{ s: [ { i, v: [数值...] } ] }` 的节点都按一串 K 线处理，
   所以 `du`、`timescale_update` 以及将来新增的方法名都能吃到，协议改版时不容易碎。

`v` 数组约定为 `[t, o, h, l, c, v]`（容忍 5 元素的无成交量形态），`t` 是 **Unix 秒**。

## 数据契约

`data/bars.sqlite`

- `bars(symbol, series, t, interval, o, h, l, c, v, source, raw, seen_at)`，
  主键 `(symbol, series, t)`，`ON CONFLICT DO UPDATE` —— 正在走的那根 K 线收到 `du` 更新时覆盖旧值。
  `source` 记录它来自哪个方法，`raw` 保留原始数值数组以便将来重新推导。
- `series_meta` 汇总每个 series 的区间、根数，以及**从相邻时间戳众数差反推出来的周期**
  （图表 URL 里没有 interval，K 线自己会说话）。

导出 CSV 列：`symbol,series,t,iso,o,h,l,c,v`。

## 自测

```bash
node test/selftest.mjs
```

19 项，覆盖切帧（多段串接 / 裸心跳 / 中文负载 / 长度不符降级）、抽 K 线（增量、无成交量、
非法值丢弃、指标序列不误抽）、入库（幂等覆盖、周期反推），以及两个真实样本回归用例：
常量 `bar_close_time` 不得产生 per-bar 周期、`st1` 指标负载不得被当成 K 线。
这部分不依赖 Chrome，可以离线验证。

## 数据现状

```
$ node bin/tv.mjs stats
总 K 线根数：301
CME_MINI:MNQ1! · series sds_1 · 5m
   301 根  2026-10-14 23:45:00 → 2026-10-16 01:45:00
```

## 实测确认（2026-10-16，CME_MINI:MNQ1! 5 分钟图）

原先列为"待确认"的三件事，抓了一份真实流量后都有了答案：

1. **prodata 不做压缩、不做二进制封装。** 585 帧全部是 opcode=1 文本，拆出 910 个 JSON 段落，
   0 个无法解析。所以不需要额外的解压层。
2. **抓包里没有 `symbol_resolved`。** 品种名只能从图表页 URL 的 `?symbol=` 取（`symbolFromUrl`）。
   代码里的 series→symbol 映射逻辑保留着，是为了将来遇到别的图表配置时能自动生效。
3. **初始加载 300 根**（该 5 分钟图覆盖约 25 小时）。想要更长历史必须主动发 `request_more_data`。

另外几个只有真实流量才会暴露的事实：

- **Chrome 只开放浏览器级 WebSocket，`/json/version` 与 `/json/list` 一律 404。**
  页面发现只能走 `Target.getTargets`，不能再用老版的 HTTP 发现接口。
- **`Network.enable` 之前就已建立的 WebSocket，Chrome 不会补发 `webSocketCreated`。**
  结果是帧照样能收到，但帧上的 `url` 为空。要拿到连接 URL（以及连上瞬间的握手），
  必须 `--reload` 让连接重建。
- 帧的 `p[0]` 是 chart session id，形如 `cs_gaSTXIAFy41H`，不是品种名。

## 踩过的坑

**`lbs.bar_close_time` 不是这根 K 线的收盘时间。** 它挂在 series 容器上，整段抓包里只有一个取值
（`1792115700` = 图表右侧可视边界，TradingView 会在最后一根 K 线右侧留白），
拿它减 `t` 会得到 94200s / 93900s / 15m 这种随时间线性漂移的垃圾周期。
我一开始就是这么写的，是数据把这个假设打翻了。**周期只能靠相邻时间戳的众数差反推**
（`inferInterval`，实测得到正确的 300s → `5m`），`test/selftest.mjs` 里已锁上这个回归用例。

**`clearInterval` 的作用域写错会让进程永不退出。** 曾写成
`.finally(() => clearInterval(tick))`，`tick` 在 executor 外不可见 → 抛 ReferenceError →
跳过 `browser.close()` → WebSocket 一直开着 → 进程挂死，工具侧只能 SIGTERM，
未排空的 stdout 全丢。现在计时器与 `clearInterval` 同作用域，清理放在 `try/finally`，
并且 CLI 出口加了一道"stdout 排空后再退出"的保险。

## 实际抓到的协议面貌

```
~m~202~m~{"m":"du","p":["cs_gaSTXIAFy41H",{"sds_1":{"s":[{"i":300,"v":[1792114800,30067.25,30067.25,30056,30060.75,843]}],
                                                   "ns":{"d":"","indexes":"nochange"},"t":"s1",
                                                   "lbs":{"bar_close_time":1792115700}}}]}
```

该图表上出现的方法（按频次）：`qsd`(665) · `du`(162) · `quote_completed`(15) ·
`quote_remove_symbols`(13) · `quote_add_symbols`(8) · `create_study`(6) · `study_loading`(6) ·
`study_completed`(6) · `create_pointset`(5) · `quote_create_session`(4) · `timescale_update`(2) ·
`set_auth_token`(1)

- `timescale_update` 是初始整段，`du` 是增量，两者共用同一形状，都按 `(symbol, series, t)` 覆盖式入库。
- `qsd` 是报价（盘口/成交），不是 K 线，按形状不会被误抽 —— 它没有 `s` 数组。
- `st1` / `st2` 是**指标**序列（用 `st` 键而不是 `s`），同样不会被误抽成价格 K 线。
  想去抓指标值的话，改 `extractBars` 接受 `st` 键即可，那是另一个需求。
- 三条 WebSocket：`prodata`（图表数据）、`pushstream/private_feed`、`pushstream/public`
  （后两条是通知/告警，与行情无关）。

## 注意

抓取属于对 TradingView 服务条款的灰色操作。低频、小批量自用风险可控；不要高频批量拉，
不要全品种刷。`capture` 默认只旁路监听，不改动页面业务逻辑。
