# tradingview-j

通过 CDP 接入**你本人已登录的 Chrome**，旁路抓取 TradingView 图表页的 WebSocket 行情帧，
解码成 OHLCV 并持续增量入库。目标是**把一条时间轴尽量填满**，用于学习与研究。

零第三方依赖（Node 22 自带的 `WebSocket` + `node:sqlite`，前端是 Vue 3 + Tailwind 的
**零构建**组合，两个库直接放在 `web/vendor/`，不联网、不打包）。

目标连接：

```
wss://prodata.tradingview.com/socket.io/websocket?from=chart/EuR5QG29/&date=...&type=chart&auth=sessionid
```

`auth=sessionid` 表示鉴权走 Cookie —— 所以必须借你浏览器的会话，这也决定了接入方式的选择。

---

## 跑起来（网页版，推荐）

```bash
npm start          # 等价于 node server/index.mjs，默认 http://127.0.0.1:8787
```

浏览器打开后是一个三页签的工作台：

| 页签 | 内容 |
|---|---|
| **抓取控制** | 开始/停止抓取、自动回拉的开关与参数、浏览器接入诊断、实时运行日志（SSE 推送） |
| **覆盖视图** | 抓取日历（月历热力图）+ 时间轴（每日覆盖条带）+ 缺口清单 + 断档清单 |
| **数据管理** | 品种库列表（一个品种一个 `.sqlite`）、原始抓包文件、入库、导出 CSV |

只绑 `127.0.0.1`，不对外监听。改端口：`npm start -- --port 9000`，或设 `TV_PORT`。

### 页面上能干的四件事

1. **启停抓取** —— 「开始抓取」按钮背后是 `CaptureSession`，和 CLI 的 `watch` 是同一个控制器。
   点下去会去接你那个正在登录的 Chrome，把图表页的 WS 帧接管过来，边抓边入库。
2. **自动回拉开关** —— 默认**关闭**。开启后程序会主动拖动图表逼页面去请求更早的历史；
   关闭时**只被动记录**，你在图表上手动回放即可，程序不向服务器多发一个请求。
   **运行中可以随时切**（不需要重启会话），这是避风控最主要的一道闸。
3. **日历 + 时间轴看覆盖** —— 见下面「覆盖视图」一节。
4. **改参数** —— 每步间隔、最多步数、静默判停、会话时长上限等；「保存为默认」写进 `data/settings.json`。

### 命令行入口仍然保留

网页跑不动或想写脚本时，`bin/tv.mjs` 的命令一个都没少（详见下面的命令清单）：

```bash
npm run tv -- doctor          # 诊断 CDP 能不能接上，卡在哪一步
npm run tv -- backfill --max-steps 40
npm run tv -- watch
npm run tv -- coverage
```

---

## 存储：一个品种一个库

以前所有品种混在 `data/bars.sqlite` 里，删一个品种要动全库，导出要带 `WHERE symbol=`。
现在拆开：

```
data/
  db/
    index.json              品种 → 文件名的索引（含真实品种名）
    CME_MINI_MNQ1!.sqlite   一个品种一个库，文件名是品种名做 slug 化的结果
    ...
  raw/
    CME_MINI_MNQ1!/         该品种的原始抓包（ndjson），按品种分子目录
    *.ndjson                历史遗留的散落文件，仍然能扫到
    index.json              抓包文件 → 品种 的索引
  ingest-state.json         入库台账（文件 size/mtime）
  ui-settings.json          网页上"保存为默认"的那份设置
```

几个实现上的决定：

- **slug 只做文件系统需要的最小替换**（`<>:"/\|?*` 和控制字符换成 `_`），不做拼音/转写，
  保留 `!`、`:` → `_` 之后的可读性。所以 `CME_MINI:MNQ1!` → `CME_MINI_MNQ1!.sqlite`。
- **Windows 文件名大小写不敏感**，`AAPL` 和 `aapl` 会撞到同一个文件。`fileForSymbol` 检测到
  slug 已被**别的**品种占用时追加 `~<sha1 前 6 位>`，再撞就 `-2`、`-3`。
- **库里再存一份真实品种名**（`meta.symbol`）。文件名是有损的，`_` 和 `:` 分不出来，
  汇总展示时要读回原始值。
- `summarize()` 同时给两个数：`bars` 是 `COUNT(DISTINCT t)`（去重后的根数），`rows` 是物理行数。
  两者不等就说明发生过 series 换号重入库。

### 从旧库迁移

启动服务时会自动检测 `data/bars.sqlite`，有待迁移内容就在页面顶部弹提示，点「立即迁移」即可。
也可以手动：

```bash
npm run migrate        # = node bin/tv.mjs migrate；旧库只读打开，绝不删改
```

- 旧库用 `{ readOnly: true }` 打开，全程**不写、不删、不重命名**。迁移完旧文件还在。
- 幂等靠 `data/db/.migrated-from-legacy.json` 记 `legacySize` / `legacyMtimeMs`；
  旧库没变过就直接跳过。
- 迁移会把旧库里的每个品种分发到各自的文件，并顺便把散落的历史抓包文件认领到
  `data/raw/<slug>/` 名下。

---

## 覆盖视图：日历 + 时间轴

两个视图**共用同一份逐格状态位图**，一次算完分别渲染，不用两套逻辑（也就不会两套对不上）。

先把这一天按周期切成槽位：`bucketSeconds()` 在阶梯
`60/120/180/300/600/900/1800/3600/7200/14400/43200/86400` 秒里向上取一个合适的桶宽 ——
5 分钟数据就是 300 秒，一天 288 格。每格有四种状态：

| 状态 | 含义 | 颜色 |
|---|---|---|
| `'0'` | 非交易时段（该星期几的这个时段历史上从不出现） | 灰 |
| `'1'` | 有数据，且参照说应该有 | 蓝（有数据） |
| `'2'` | **参照说应该有，但实际没有** | 红（应有但缺） |
| `'3'` | 有数据，但参照里没这个时段（异常时刻 / 参照样本不足） | 蓝 |

- **月历**：每格显示当天根数 + 一条完整度进度条，颜色代表状态。点某天看「单日明细」：
  该天每个连续时段的起止。
- **时间轴**：上面是「每个时段被覆盖了多少天」的直方图（一行 288 格，一眼看出是不是每天都缺
  开盘那半小时），下面每天一行、每格一个 5 分钟。鼠标悬停看具体时段，点一下切到那天的明细。
- 条带宽度按容器算出 `bucketW` 并**钳在 `[1.4, 6]` px**：太窄看不清，太宽一行放不下。
  放不下就横向滚动，不去硬挤（硬挤会把格子压到亚像素，位图直接糊成一片）。

「参照」是从数据自身学的时段模式（见下节）。视图里会给两个诚实的提示：
参照样本不足时打 ⚠ 说明覆盖率偏乐观；断档列表完全不做交易时段假设，用来交叉验证。

---

## 先看结论：怎么把历史拉回来

TradingView 的图表是**按需**向服务器要数据的：只有当**可视范围走出已缓存的那一窗**时，
页面才会发 `request_more_data`，服务器回一段 `timescale_update`。实测每次请求回约 90~300 根。

所以"填满时间轴"的本质就是**反复把图表往回拨**。两条路：

**手动（默认，最安全）**：关掉自动回拉 → 开始抓取 → 去 Chrome 里打开回放，自己往各个日期跳。
程序只被动记录，你在图表上做什么它就收什么，不额外发请求。

**自动**：打开自动回拉，程序反复往回拖 → 等数据到齐 → 统计库内新增 → 连续若干步没有新数据
就收工。想一次性跑完用 CLI 的 `backfill`：

```bash
npm run tv -- backfill --max-steps 40
```

它会反复把图表往回拖，连续多步没有新数据就停，最后自动把视图拨回现在。中途 Ctrl+C 也安全
（数据已落库）。

只想手工拨一下试试，用最小原语：

```bash
npm run tv -- pan --dx 1200 --times 3    # dx > 0 往更早看；--dx -1200 往回看
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

### 命令清单

| 命令 | 作用 |
|---|---|
| `serve` | 起网页工作台（`npm start` 就是它） |
| `doctor` | 诊断 CDP 能不能接上，卡在哪一步 |
| `targets` | 列出可抓标签页 |
| `symbols` | 列出现有的品种库 |
| `migrate` | 把旧版混合库拆成一个品种一个文件 |
| `watch` | **常驻**：抓到帧即解码入库，定期打印覆盖度 |
| `backfill` | **自动回填**：程序自己反复往回拨图表 |
| `capture` | 只抓帧落盘（`data/raw/<slug>/*.ndjson`），不入库 |
| `ingest` | 解码入库；默认只处理新增/变动过的抓包文件 |
| `raw` | 列出抓包文件及其归属品种 |
| `coverage` | **覆盖度报告**：哪里还是空的、下一个该补哪天 |
| `calendar` | 终端版日历条带（用 `█ ·▒` 字符画） |
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
$ npm run tv -- coverage
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

每个品种库（`data/db/<slug>.sqlite`）：

- `meta(key, value)` —— 至少存 `symbol`（真实品种名）与 `last_write_at`。
- `bars(symbol, series, t, interval, o, h, l, c, v, timescale, source, raw, seen_at)`，
  主键 `(symbol, series, t)`，`ON CONFLICT DO UPDATE` —— 正在走的那根 K 线收到 `du` 更新时覆盖旧值。
- `series_meta` 汇总每个 series 的区间、根数，以及**从相邻时间戳众数差反推出来的周期**。

`data/ingest-state.json` 是入库台账，记下每个抓包文件的 size/mtime，让 `ingest` 默认只处理
新增或变动过的文件。想全量重扫用 `--all`。

导出 CSV 列：`symbol,interval,t,iso,o,h,l,c,v`。

### series id 会变，别把它当身份

`sds_1` / `sds_2` 这种 series id 是**每条 WebSocket 连接自己发的临时订阅号**，页面重连后会换号。
实测：页面重连后同一个 5 分钟图从 `sds_1` 变成 `sds_2`，于是同一根 K 线在库里留下两行（内容一致）。

所以：

- 主键里保留 `series`（原始保真，双图不同周期时不会互相覆盖）；
- `coverage` / `calendar` / 网页汇总一律**先去重**再算根数。不去重的话"根数"会虚高，
  断档计算还会看到一堆 0 间隔。
- `export` 默认按 `(symbol, interval, t)` 收敛重复行，用 `--raw` 可关闭。
- `stats` 和网页的品种表都会把多个 series 列出来并提示这件事。

---

## 自测

三层，都能离线跑：

```bash
npm test                        # 前两层
node test/selftest.mjs          # ① 后端纯 Node：协议 / 存储 / 入库路由 / 覆盖度与日历 / 迁移
node test/ui-selftest.mjs       # ② 前端纯函数：切出 app.js 里 createApp 之前那段直接 eval
node test/e2e.mjs               # ③ 端到端：真后端 + 独立无头 Chrome
```

**为什么必须有第三层。** jsdom 没有布局引擎也没有 CSSOM，Tailwind 运行时注入的样式它一概读不到；
「两栏有没有并排」「开关是不是 46×26」「canvas 有没有被压成 0 宽」只有真浏览器答得了。
页面的 JS 报错（模板里引用了不存在的字段）也只有真跑才暴露。

`test/e2e.mjs` 刻意用**独立的无头 Chrome + 临时 profile**，不碰你那个正在登录的浏览器：
不去创建/关闭你可见的标签页，视口能钉死，并且用 `TV_CDP_PORT` 把服务的调试端点指到这个无头实例，
于是「开始抓取」链路也能真实走一遍，不需要你去点 Chrome 的授权弹窗。图表页用一个本机 URL 冒充
（`pickTarget` 按 URL/标题子串匹配，`/fake/tradingview.com/chart/...` 就够了），完全不依赖外网和登录态。
截图落在 `data/e2e/*.png`，深浅色各一套，方便肉眼过。

---

## 实测确认（2026-10-16，CME_MINI:MNQ1! 5 分钟图）

1. **prodata 不做压缩、不做二进制封装。** 全部是 opcode=1 文本，910 个 JSON 段落 0 个无法解析。
2. **抓包里没有 `symbol_resolved`。** 品种名只能从图表页 URL 的 `?symbol=` 取。
3. **初始加载 300 根**（约 25 小时）。更长历史靠 `request_more_data`。
4. **拨动图表确实会触发 `request_more_data`。** 实测拖 3 次，`sds_1` 从 302 根涨到 578 根，
   最早从 `10-14 23:45` 推到 `10-13 08:40`。这是整个"填时间轴"方案的立足点。
5. **Chrome 只开放浏览器级 WebSocket，`/json/version` 与 `/json/list` 一律 404。**
   页面发现只能走 `Target.getTargets` + `Target.attachToTarget`（flatten）。
6. **`Network.enable` 之前就已建立的 WebSocket，Chrome 不会补发 `webSocketCreated`。**
   帧照样收到，但帧上的 `url` 为空。要拿连接 URL 必须 `--reload` 让连接重建。
7. 帧的 `p[0]` 是 chart session id（形如 `cs_QAPb9LkfpOwN`），不是品种名。
8. **`Target.createTarget` 不接受 `width`/`height`** —— Chrome 155 直接回
   `Target position can only be set for new windows`。视口要另外用 `Emulation.setDeviceMetricsOverride`。

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

### 项目化之后新踩的三个

**① 迁移时用旧快照回写索引，把刚登记的映射抹掉了。**
`migrateLegacy` 在循环开始前 `loadIndex` 拿了一份快照，循环里 `fileForSymbol` 各自读-改-写了
`index.json`，最后又用那份旧快照 `saveIndex` 盖回去 → 迁移完索引是空的 `{"symbols":{}}`，
而库文件都在。**读-改-写的接口不能在循环外缓存快照**，改成循环结束后重新 `loadIndex` 只补缺的条目。
排查手法：清空 `data/db/*` 重跑一遍，看索引里到底登了什么。

**② 日历把「整日无数据」短路掉了，正好抹掉了最该看见的洞。**
`buildCalendar` 原本写的是 `if (!bits) { states = '0'.repeat(n); return }` —— 一个完全没有数据的
日子恰恰最该被标成"应有但缺"，短路掉等于把整日空洞从热力图上抹掉。改成所有日子统一走
`expectedBuckets.has(sec)` 四态判定，`'0'/'1'/'2'/'3'` 一个分支不落。

**③ 覆盖度的「根数」没去重，虚高。**
series 换号会让同一根 K 线在库里留两行，`coverage` 直接 `ts.length` 就把 2274 根报成 2576 根，
断档计算还会看到一串 0 间隔。现在 `analyze()` 开头就 `[...new Set(...)]`，
`summarize()` 用 `COUNT(DISTINCT t)`，并把物理行数单独作为 `rows` 暴露出来 ——
两个数不一致本身就是"发生过重连"的信号，不该藏起来。

**④ 前端零构建的三个小坑。**

- **Tailwind 4 会 tree-shake 主题变量。** `@theme` 里定义的变量，如果没有任何一个工具类
  实际用到它，就**不会被输出到 `:root`**。`--color-accent` 正是这种：全站只有 canvas 和我自己
  写的 CSS 在用它，没有 `bg-accent` 之类的类名，于是它被删了。而 canvas 是用
  `getComputedStyle` 读变量的 —— 读不到拿到空串，`ctx.fillStyle = ''` 又是**静默忽略**，
  结果整层图形被跳过：浅色模式下时间轴里 2274 个「有数据」格子一个都没画出来，
  图例色块和直方图柱也一起消失。修法是 `@theme static`（强制全量输出），
  外加 `canvasPalette()` 逐项兜底 + 缺变量时 `console.error` ——
  深色模式当时看着是正常的，因为 `html.dark { … }` 是手写 CSS，变量一直在，所以只坏了一半。

- **Tailwind 4 里未分层的普通 CSS 永远赢过分层 CSS**，所以 `style.css` 里只写
  `.card`/`.btn`/`.switch` 这类结构性样式，绝不写 margin/width（写了就盖掉工具类）。

- **Tailwind 的 preflight 不设 `font-size`**，必须在 `style.css` 显式写 `body { font-size: 14px }`，
  否则整站还是浏览器默认的 16px —— 这个由 e2e 里那条「body 的 font-size 是 14px」盯着。

---

## 注意

抓取属于对 TradingView 服务条款的灰色操作。低频、小批量自用风险可控；
**不要高频批量拉，不要全品种刷。**

自动回拉默认关闭就是这个原因：主动操作图表会产生非人类的交互序列，高频使用有被风控的可能。
开启时建议把「每步间隔」保持在 2 秒以上、给「最多步数」设个上限，别挂机跑一整夜。

`backfill` 会连续操作你的图表页面（默认结束后拨回原位）。如果只是想看看，先用
`--max-steps 3` 小步试。
