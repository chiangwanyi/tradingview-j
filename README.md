# tradingview-j

通过 CDP 接入**你本人已登录的 Chrome**，旁路抓取 TradingView 图表页的 WebSocket 行情帧，
解码成 OHLCV 并持续增量入库。目标是**把一条时间轴尽量填满**，用于学习与研究。

零第三方依赖（Node 22 自带 `WebSocket` 与 `node:sqlite`）。

目标连接：

```
wss://prodata.tradingview.com/socket.io/websocket?from=chart/EuR5QG29/&date=...&type=chart&auth=sessionid
```

`auth=sessionid` 表示鉴权走 Cookie —— 所以必须借你浏览器的会话，这也决定了接入方式的选择。

---

## 先看结论：怎么把历史拉回来

TradingView 的图表是**按需**向服务器要数据的：只有当**可视范围走出已缓存的那一窗**时，
页面才会发 `request_more_data`，服务器回一段 `timescale_update`。实测每次请求回约 90~300 根。

所以"填满时间轴"的本质就是**反复把图表往回拨**。不必手动开回放，程序可以自己拨：

```bash
node bin/tv.mjs backfill --max-steps 40
```

它会反复把图表往回拖 → 等数据到齐 → 统计库内新增 → 连续若干步没有新数据就收工 →
最后自动把视图拨回现在。中途 Ctrl+C 也安全（数据已落库）。

想自己控制节奏就用常驻模式，一边手动回放/拖动，一边看覆盖度实时变化：

```bash
node bin/tv.mjs watch
```

想手工拨一下试试，就用最小原语：

```bash
node bin/tv.mjs pan --dx 1200 --times 3    # dx > 0 往更早看；--dx -1200 往回看
```

## 为什么是 CDP 这条路

| 方案 | 结论 |
|---|---|
| **CDP 接入已登录 Chrome** | **选它**。复用会话、无需重新登录、能看到二进制帧、还能主动发帧/主动拨图表 |
| Chrome 扩展猴补 WebSocket | 不用重启，但从页面外部难以主动驱动图表 |
| mitmproxy 中间人 | 要装根证书，配置重 |

一个关键事实（Chrome 155 实测确认）：**Chrome >= 136 起，给默认用户数据目录加
`--remote-debugging-port` 会被静默忽略**，端口根本不开。所以"关掉 Chrome 再加参数重启"
这条路走不通。官方替代是 Chrome >= 144 的开关：

```
chrome://inspect/#remote-debugging
  -> 勾选 "Allow remote debugging for this browser instance"
```

能在**当前运行中的实例**上生效，不需要重启、不需要重新登录。代价是每次外部发起连接，
Chrome 会弹一次授权框要求点 Allow；每个新的浏览器会话都要重新勾一次。

---

## 跑起来

```bash
# 0. 诊断（会明确告诉你卡在哪一步）
node bin/tv.mjs doctor

# 1. 先去 Chrome 里打开 chrome://inspect/#remote-debugging 勾选开关
node bin/tv.mjs targets

# 2A. 全自动回填历史（推荐起点）
node bin/tv.mjs backfill --max-steps 40

# 2B. 或者常驻，自己去图表上回放/拖动
node bin/tv.mjs watch

# 3. 看还缺哪里
node bin/tv.mjs coverage

# 4. 导出
node bin/tv.mjs export --out data/bars.csv
```

### 命令清单

| 命令 | 作用 |
|---|---|
| `doctor` | 诊断 CDP 能不能接上，卡在哪一步 |
| `targets` | 列出可抓标签页 |
| `watch` | **常驻**：抓到帧即解码入库，定期打印覆盖度 |
| `backfill` | **自动回填**：程序自己反复往回拨图表 |
| `capture` | 只抓帧落盘（`data/raw/*.ndjson`），不入库 |
| `ingest` | 解码入库；默认只处理新增/变动过的抓包文件 |
| `coverage` | **覆盖度报告**：哪里还是空的、下一个该补哪天 |
| `stats` | 库内汇总 |
| `export` | 导出 CSV（默认按 `symbol+interval+t` 收敛重复行） |
| `pan` | 拖动图表（最小操作原语） |
| `shot` | 截图，用来肉眼确认图表停在哪 |
| `send` / `eval` / `cdp` | 主动发帧 / 页面求值 / CDP 透传，调试用 |

---

## 覆盖度报告：为什么不能简单地"按周期步进"

期货有每日结算间歇（CME 每天 21:00–22:00 UTC），还有周末休市。如果机械地从最早按 5 分钟
步进到最晚，这些正常休市全会被报成缺失，噪声大到看不出真正漏抓的是哪天。

`coverage` 的做法是**从数据自身学习时段模式**：

1. 对每个星期几，统计历史上出现过哪些 `HH:MM` 时段；
2. 某时段在多数同类日子里都出现 → 视为"应当存在"；
3. 把区间内每个日历日与该模式比对，列出缺哪些时段。

因为模式是从数据里学的，周末和休市天然不产生噪声，而真正漏抓的日子会被点名。

两个诚实的边界，报告里都会写明：

- **参照样本不足**：某个星期几只见过 1~2 天时，模式几乎等于照抄那一天，报出来的高覆盖率
  没有意义。此时报告会打上 ⚠，提示这个数字只能当"有没有明显大洞"的粗筛。
- **断档列表**：另有一节完全不做交易时段假设，纯看相邻时间戳被切成几段、最长空白多久。
  它是模式比对的交叉验证 —— 模式说"完整"但断档列表里躺着一段几十小时的空白，
  就说明模式样本太少导致了假绿灯。

```
$ node bin/tv.mjs coverage
CME_MINI:MNQ1! · 5m · 1049 根 · 相邻间隔 300s
   范围   2026-10-13 08:40:00 → 2026-10-16 02:05:00（UTC）
   覆盖   跨 3 天 / 有数据 3 天 · 应到 ... 根 · 实到 ... 根 · 缺 ... 根（...%）
   参照   每个星期几的样本天数：周一×1 周二×1 周三×1 周四×1 周五×1
   ⚠ 参照样本不足 ...
   断档   时间轴被切成 N 段，最长的 8 处空白：
     2026-10-14 20:55:00 → 2026-10-14 22:00:00  1.1 小时  约缺 12 根
```

---

## 帧格式

外层是自定义分帧，不是裸 JSON：

```
~m~<负载字符长度>~m~{"m":"du","p":[...]}    消息
~h~<n>                                     心跳，必须原样回送，否则连接会被服务端断开
```

三个坑，代码里都已处理：

1. **心跳会直接贴在 JSON 负载后面**（心跳没有 `~m~` 头）。按帧头切分会把
   `{"m":"heartbeat","p":[]}~h~7` 当成一段导致 JSON 解析失败，所以 `splitFrame` 是正向扫描的 tokenizer。
2. **声明长度的语义**（字符还是字节）不保证一致。长度对不上时退化为"切到下一个帧头之前"，
   宁可多吞也不丢数据。
3. **取 K 线不硬编码方法名**。任何形状为 `{ s: [ { i, v: [数值...] } ] }` 的节点都按一串 K 线处理，
   所以 `du`、`timescale_update` 以及将来新增的方法名都能吃到，协议改版时不容易碎。

`v` 数组约定为 `[t, o, h, l, c, v]`（容忍 5 元素的无成交量形态），`t` 是 **Unix 秒**。

---

## 数据契约

`data/bars.sqlite`

- `bars(symbol, series, t, interval, o, h, l, c, v, timescale, source, raw, seen_at)`，
  主键 `(symbol, series, t)`，`ON CONFLICT DO UPDATE` —— 正在走的那根 K 线收到 `du` 更新时覆盖旧值。
- `series_meta` 汇总每个 series 的区间、根数，以及**从相邻时间戳众数差反推出来的周期**。
- `data/ingest-state.json` 是入库台账，记下每个抓包文件的 size/mtime，
  让 `ingest` 默认只处理新增或变动过的文件。想全量重扫用 `--all`。

导出 CSV 列：`symbol,interval,t,iso,o,h,l,c,v`。

### series id 会变，别把它当身份

`sds_1` / `sds_2` 这种 series id 是**每条 WebSocket 连接自己发的临时订阅号**，页面重连后会换号。
实测：页面重连后同一个 5 分钟图从 `sds_1` 变成 `sds_2`，于是同一根 K 线在库里留下两行（内容一致）。

所以：

- 主键里保留 `series`（原始保真，双图不同周期时不会互相覆盖）；
- `coverage` 按 **symbol** 聚合，不受换号影响；
- `export` 默认按 `(symbol, interval, t)` 收敛重复行，用 `--raw` 可关闭。
- `stats` 会把多个 series 列出来并提示这件事。

---

## 自测

```bash
node test/selftest.mjs
```

27 项，不依赖 Chrome，可离线验证：

- **切帧**：多段串接 / 裸心跳 / 中文负载 / 长度不符降级
- **抽 K 线**：增量、无成交量、非法值丢弃、指标序列不误抽
- **入库**：幂等覆盖、周期反推
- **覆盖度**：从 4 周合成数据里精确找出被抽掉的那一天；断档交叉验证；只有 1 天样本时判定参照不足
- **导出收敛**：换 series 号的重复行被合并、保留最后见到的版本、不同周期不互相覆盖
- **两个真实样本回归**：常量 `bar_close_time` 不得产生 per-bar 周期、`st1` 指标负载不得被当成 K 线

---

## 实测确认（2026-10-16，CME_MINI:MNQ1! 5 分钟图）

1. **prodata 不做压缩、不做二进制封装。** 全部是 opcode=1 文本，910 个 JSON 段落 0 个无法解析。
2. **抓包里没有 `symbol_resolved`。** 品种名只能从图表页 URL 的 `?symbol=` 取。
3. **初始加载 300 根**（约 25 小时）。更长历史靠 `request_more_data`。
4. **拨动图表确实会触发 `request_more_data`。** 实测拖 3 次，`sds_1` 从 302 根涨到 578 根，
   最早从 `10-14 23:45` 推到 `10-13 08:40`。这是整个"填时间轴"方案的立足点。
5. **Chrome 只开放浏览器级 WebSocket，`/json/version` 与 `/json/list` 一律 404。**
   页面发现只能走 `Target.getTargets`。
6. **`Network.enable` 之前就已建立的 WebSocket，Chrome 不会补发 `webSocketCreated`。**
   帧照样收到，但帧上的 `url` 为空。要拿连接 URL 必须 `--reload` 让连接重建。
7. 帧的 `p[0]` 是 chart session id（形如 `cs_QAPb9LkfpOwN`），不是品种名。

出现的方法（按频次）：`qsd`(665) · `du`(162) · `quote_completed`(15) · `quote_remove_symbols`(13) ·
`quote_add_symbols`(8) · `create_study`(6) · `study_loading`(6) · `study_completed`(6) ·
`create_pointset`(5) · `quote_create_session`(4) · `timescale_update`(2) · `set_auth_token`(1)

- `timescale_update` 是整段，`du` 是增量，两者共用同一形状。
- `qsd` 是报价不是 K 线（没有 `s` 数组）；`st1`/`st2` 是**指标**序列（用 `st` 键），都不会被误抽。
- 三条 WebSocket：`prodata`（图表数据）、`pushstream/private_feed`、`pushstream/public`（通知）。

---

## 踩过的坑

**`lbs.bar_close_time` 不是这根 K 线的收盘时间。** 它挂在 series 容器上，整段抓包里只有一个取值
（`1792115700` = 图表右侧可视边界），拿它减 `t` 会得到 94200s / 15m 这种随时间线性漂移的垃圾周期。
我一开始就是这么写的，是数据把这个假设打翻了。**周期只能靠相邻时间戳的众数差反推**，
`selftest` 里已锁上回归用例。教训：**对协议字段语义的猜测不该写进自测当事实，那会制造虚假的绿色。**

**`clearInterval` 的作用域写错会让进程永不退出。** 曾写成 `.finally(() => clearInterval(tick))`，
`tick` 在 executor 外不可见 → 抛 ReferenceError → 跳过 `browser.close()` → WebSocket 一直开着 →
进程挂死。现在计时器与 `clearInterval` 同作用域，清理放 `try/finally`，CLI 出口再加一道
"stdout 排空后再退出"的保险。

**`makeWriter.close()` 内部已经关了数据库**，外层再 `db.close()` 会抛 `ERR_INVALID_STATE`。

**"拖动没反应"要区分两种原因。** `backfill` 里有一轮 0 新增：当时只发出 2 帧，
说明拖拽压根没送达图表（后台标签页渲染被节流）。而现在 `backfill` 会在每次拖动前
`Page.bringToFront`、并统计图表发出的 `request_more_data` 次数 —— 没发请求和"已到边界"
是两件完全不同的事，日志里分开呈现。

---

## 注意

抓取属于对 TradingView 服务条款的灰色操作。低频、小批量自用风险可控；
**不要高频批量拉，不要全品种刷。**

`backfill` 会连续操作你的图表页面（默认结束后拨回原位）。如果只是想看看，先用
`--max-steps 3` 小步试。
