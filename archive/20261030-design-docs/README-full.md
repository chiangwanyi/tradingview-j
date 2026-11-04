# tradingview-j

一个仓库两块东西，**互不引用**：

- **Go 侧（`main.go` / `internal/` / `web/`）** —— 旁路抓你**本人已登录的 Chrome** 里 TradingView 图表页的
  WS 行情帧，解 5 分钟 K 线落 CSV，并做本地复盘。零第三方依赖，产物是根目录单文件
  `tv-capture-v<版本>.exe`（当前 `0.31.0`）。只**被动**监听：不注入脚本、不操作图表、无回拉。
- **Spring 侧（`src/` / `pom.xml` / `webapp/`）** —— Go 版复盘系统的迁移目标，SQLite 落库，端口 8791。
  含 PF（Prop Firm）账户/模板、只读行情接口；交易撮合仍是方案，见 §三。

---

# 一、Go 侧：抓取 + 复盘

## 跑起来

**Chrome 开远程调试**（每个浏览器会话一次）：在已登录 TradingView 的 Chrome 里访问
`chrome://inspect/#remote-debugging` → 勾 **Allow remote debugging for this browser instance** →
打开**图表页**（URL 带 `/chart/`）、周期切 **5 分钟**。

> Chrome 136 起给默认用户目录加 `--remote-debugging-port` 会被**静默忽略**，只能走这个开关：
> 代价是每次外部连进来弹一次 Allow。

另需 **Go 1.26+** 和 `go install github.com/tc-hib/go-winres@latest`（构建期工具）。

```bat
build.cmd               :: 前端 + exe 一次做完，任一阶段失败就地中断
build.cmd --no-web      :: 只编 exe，沿用上次的前端产物
```

三阶段固定顺序：`cd web && npm run build`（→ `web\dist\`，**不进 exe**）→ `go-winres make
--file-version=<v> --product-version=<v>`（→ `.syso`）→ `go build -ldflags "-X main.version=<v>"
-o tv-capture-v<v>.exe .`（→ 根目录 exe）。

```
1 启动抓取（录 CSV + 开分析页）    2 复盘（回放/标注/模拟交易/交易记录）    3 退出
```

版本号只有 `VERSION` 一个来源，落到三处（文件名 / exe 属性 / 程序内横幅与网页页脚）。**升版本 = 改
VERSION 再重跑 `build.cmd`**；`.syso` 是编译期快照，别手改别提交。`outDir`/`rawDir` 相对 **exe 所在目录**
—— 换目录放 exe 等于换一份数据。

## 菜单 1：抓取 + 分析页

选 1 后：抓取开始等调试端口和图表页（没开就一直等，每 5 秒重试），分析页在 `127.0.0.1:8765` 起来并替你
打开。**你要做的只有一件事**：在 Chrome 里往回拖图表，或用回放模式跳日期 —— 图表走到没缓存的范围时页面
自己会要数据，程序录下来，分析页根数自己涨。`Ctrl+C` 先刷盘、再关分析页，回到菜单。开多个图表标签页会
**全部挂上**、各自独立录。

分析页对数据目录做**只读**扫描（不建目录、不写索引），只绑 `127.0.0.1`、**没有能改数据的接口**：每个标的
一张月历，格子里是**当天 K 线根数**（标红 = 与星期应有根数不符或坏行，标黄 = 各标的根数不一致，灰 = 没
数据）；点某天出明细（根数、应有根数、首尾根、**每段断档的起止**）；异常日**变淡不隐藏**。

**实时是三层接力，没有推数据的通道**：`store` 每 `flushSeconds`(1s) 重写脏日文件 → `webreport` 每次请求
先比**磁盘指纹**（名字 + 大小 + 修改时刻），一样发缓存、不一样重扫 → 页面每 2s 拉 `/api/analysis`。数字
永远是磁盘那一刻的样子，只是看得勤。`Scanner` 记住每份文件上次的摘要与指纹，只重解析变过的
（44MB/3300 份：全量 1.2s → 无变化 6ms）。

## 菜单 2：复盘

本地网页逐根回放 `data/` 的 K 线，画标注、做模拟交易。和菜单 1 的区别：那个是**只读报表**，这里是
**有状态的应用**（实例/标注/挂单/成交写 `state/`），但**仍只读数据目录**，一个 CSV 都不改。

- **实例**：右上「标的与实例」→ 选标的（时间留空随机）→ 新建，铺出锚点前 400 根、之后逐根揭示。
  另两入口：【最新】锚在 `data/` 最新一根；【开盘】**随机挑一交易日**、锚在第 1 根（首尾两天不参选）。
  判据是**停盘缺口**不是「0 点」：只能按「和前一根隔 65 分钟」认（`replay/session.go`）。【下一根】
  揭示一根并用其高低撮合挂单（键盘 **→** 同效，长按不连发）；【下一天开盘】跳到下一交易日第 1 根，
  那一夜**逐根撮合**过去（跳过等于抹掉夜里成交）。
- **标注**五种：趋势/射线/矩形/箭头/斐波那契。**【射线】单击完成**，其余两次单击；Shift 画水平线、
  Ctrl 吸附 OHLC；坐标严格是「K 线时间 + 价格」。点一下就能改（拖端点、拖线身、`Del` 删），选中后
  左上出现同类模板条（只换外观不动端点，浮在画布上不占工具条高度）。左上【K 线数据栏】显示该根
  **开高低收 · EMA20 · ATR · 涨跌**（涨跌是**价差**，正绿负红，首根 `—`）。
- **下单**：任意价格右键，单型按价格相对现价的方向自动定；挂单**两对**（手填 / nATR 倒推，算不出时
  置灰）。可**复制价格 / 复制时间**。挂单线可拖改价、右端「×」撤单。**有仓位时对手单旁显示该挂单成交后
  的盈亏**（多头 2 手挂卖 3 手 → 按**仓位**只算 2 手；同方向是加仓不显示），口径
  `(平 − 开) × 手数 × 每点价值`。
- **模拟交易**：限价/止损/市价，**没有止损止盈类型**（要止损就挂 `stop` 单），无初始资金、不计手续费/
  点差/滑点。浮窗有「市价退仓并取消所有挂单」（先撤光、再按现价平掉整仓）。
- **nATR 定手数**：填 **n**（止损 = n×ATR）和**最大亏损**，给出「每手亏多少 → 下几手」。例：ATR 19.24、
  n=10 → 192.4 点；MNQ 每点 $2 → 每手 $384.80；上限 $2000 → 5.197 → **5 手**（实亏 $1924）。按下时
  **止损自动挂**。
- **指标**（EMA20 / ATR / Doji X + ZigZag）在【指标 ▾】。ATR 是价格区下方**独立副图**，**Length 14 +
  RMA**（对齐 `ta.tr(true)`）；副图无价格坐标，右键下单和画图在那儿不生效。**Bar Count**：每根最低价下方
  标「当天第几根」，每张 CSV 从 #1 重数、只标单数。
- **多人**：第二个标签页是**观众**，能收不能操作；「交还/接管」切换，操作者关页自动释放。底边两个 Tab
  （模拟交易 / 操作记录）；回放中多一枚红色「回放中 x/y」。
- **回放延伸**：走到最后一步可【＋ 加载下一根】，把记录末尾之后的**真实 K 线**演出来。它是**写进记录的
  一步**（`kind=bar_extended`），能跳回能重放，也能在上面录音（每根 9 条）。**只能在末尾加载**。
- **交易记录**独立页，首页**月历按「记录日」排**（今天补做去年 1 月的行情也算今天），按盈亏染色
  （绿赚红亏 + ▼▲），写「N 笔 / ±X$」。点进**当天详情**：KPI + 六张图 + 逐笔明细，**点任一行回画布逐格
  演这一笔**。**只排版，钱仍来自后端。**

改过前端必须重新 `npm run build`；开发期 `cd web && npm run dev`（`/ws` 转 8790）。

## 配置 · 数据 · 结构

程序**不接受命令行参数**，全在 exe 同目录 `config.json`（不存在自动生成），相对路径相对 **exe 目录**。
常改的：`matchUrl`（挂哪个标签页）、`symbol`（留空自动识别）、`intervalSeconds`(300，**只收这个周期**)、
`outDir`/`rawDir`(`data`/`raw`)、`webPort`/`replayPort`(8765/8790)、`flushSeconds`(1)。

```
data/_symbols.json              标的 → 文件夹 / tick / tickValue / commission
data/CME_MINI_MNQ1!/2026-10-15.csv
state/replay-instances.json + instances/ins-1/{meta,bars,annotations,orders,trades,…}.json
presets/replay-templates.json   绘图模板（全局）·  recordings/recordings.json + rec-<ts>/…
raw/<标的>/<时刻>.ndjson
```

CSV 是 `time,open,high,low,close,volume`，**时间列是明文 `yyyy-MM-dd HH:MM`、本机时区、这根的开盘时间**。
写盘整体重写（tmp+rename），启动先读回当日文件再合并。`state/` `recordings/` `raw/` 不入库。

```
VERSION / build.cmd / winres/winres.json / main.go（只有 main() 与 version）
internal/cli       横幅、菜单循环、编排（唯一的 stdin reader）
internal/capture   常驻会话：等端口、等页面、挂监听
internal/cdp       端点发现、命令与事件、挑目标页
internal/wsclient  自写 WS 客户端（大帧 + 分片）
internal/tvproto   分帧、抽 K 线、标的解析
internal/ingest    解码：周期反推、暂存、落盘
internal/store     CSV 存储 + scan.go 只读扫描（并发 + 增量）
internal/config · rawlog · logx · wstest   配置 / 原始帧 / 日志 / 测试假服务端
internal/webreport 分析页：JSON + embed 的 static/（改了要重新 go build）
internal/replay    菜单 2：WS + 实例状态机 + 标注 + 撮合 + 机器人 + 交易记录
internal/strategy  纯函数策略：出「方向 + 止损 + 目标」
web/               复盘前端（Vue 3.5 + Pinia + TS + Tailwind 4 + Vite）
```

分层按「**谁能 import 谁**」：入口 `main` → 编排 `cli`/`capture` → 协议 `cdp`/`wsclient`/`tvproto`
与领域 `ingest`/`store`/`config`/`rawlog` → 表现 `webreport`/`replay`；`logx` 谁都能用。
**入口不搬 `cmd/`**（双击运行，且 `.syso` 只链同目录包）；**`internal/` 保持扁平**。前端 K 线
**不用图表库**（Canvas 画 K 线、SVG 画标注，共用 `chart/scale.ts`），只 `/records` 六图用 ECharts。

## 不能让步的约定

**标的名只认 `symbol_resolved`**，别在整帧里搜 `pro_name` —— 同一条 WS 上还有自选/报价的 `qsd`，带着
**别的品种**。顺序：`config.symbol` → 序列绑定 → 会话内唯一 → 页面内唯一 → URL 猜；给不出结论时 K 线
**先攒着不落盘**。**周期靠众数差反推**，按「会话 + 序列」**分别投票**（混投互相污染）；`lbs.bar_close_time`
是陷阱（图表右侧可视边界，不是收盘时间）。

**统计口径只有一份**：根数、断档、应有根数、异常判定全在 `store.Scan`；在 JS 里重算会出现两份口径，而
**同一目录给出不同根数时用户没法裁决**。**「分析」不许写任何东西**：只走 `store.Scan`（`store.Store` 一
Open 就建目录）；复盘读 CSV 只用 `ReadDayRows`/`Catalog`，唯一写入口 `persist.go`。CSV 行格式只在
`store.readDayFile` 定义一次。

**前端预览、后端定稿**：除缩放/平移/十字光标，没有操作在本地改状态；正式状态**永远来自广播**。唯一两边各
算一遍的是 Ctrl 吸附（`chart/snap.ts` 与 `instance.go:nearestOHLC` 同口径，不一致的症状是「松手后图形
跳一下」）。**指标按「是不是盘面事实」分**：EMA20/ATR14 在 Go 里算好下发（`Bar.ema20`/`Bar.atr14`），
Doji X/ZigZag 参数留前端；ATR 周期**钉在 14**，副图是布局的一部分（`scale.setAtrPane`）。**标注只有
「时间 + 价格」**：像素换窗口就错；端点可带 `offset` 往右越过最后一根，所以延伸段和 K 线严格对齐。

**撮合规则只有一份**：`instance.go`，机器人走同一套方法（策略拿不到 `Instance`，造不出成交价）。成交价
通常是挂单价、跳空取开盘价；本根挂的单从**下一根**才参与撮合。**前端不算钱**，唯一例外是 nATR 定手数
（只算手数不产生盈亏，`lib/natr.ts`，「每点价值」必须按 `tickValue / tick`，与后端 `pointValueOf` 漂了
用户就会发现「说好亏 $1924，结果不是」）——**止损价不在例外里**，前端只传距离。**一键退仓是一条消息**：
`flatAll` 先平仓再撤单、发 `flat_all`。

**交易记录挂在唯一的广播出口上**（不散在 `do*Locked` 里，否则漏一个动作就表现成「回放少一步」）；录的
是**广播原文**，回放喂给同一个 reducer。**回放延伸不复用 `bar_advanced`**（那条 payload 带持仓全量快照，
延伸段没有成交，照它更新会把仓位改写成「还开着」）；落盘用 `UpdateRecording` **不是 `Save`**（`Save` 会
按本次传入的字节清理 `audio-*.wav`，等于删光语音）。

**两条行为契约**：同组保护单（OCO）一根里只成交一张，**两张都被扎到时判止损**；**日历日最后一根
（23:55）无条件平仓 + 撤光挂单**，不留过夜。**机器人 = 策略 + 执行器 + 撮合**：策略出「方向 + 止损价 +
目标价」（**绝对价格不是距离**，换算散两边迟早有一边写反），实现 `OnBar` 即可。

## 测试 · 改代码前

`go test ./...`（全部离线，不需要浏览器）· `cd web && npm run type-check && npm run lint && npm run build`。
前端代码写完后**应使用浏览器实际打开页面**校验渲染、交互与运行时报错。抓取侧的离线测试用**假 CDP 服务端
扮演 Chrome**（`internal/wstest`），把**真实抓包**（`testdata/`）当帧事件推下去：握手、分帧、抽 K 线、
周期判定、品种识别、落盘、留档全跑一遍。

- **版本号只改 `VERSION`** 且必须重新构建；**`build.cmd` 必须纯 ASCII + CRLF**（有测试拦）。
- **菜单只能有一个 stdin reader**，否则缓冲里的字符被绕过，表现成「按一次回车跳过两项」。
- **网页版不许有算数字的代码**：改显示格式或日期算术要同步 `format.test.cjs` 的期望值（唯一事实来源）。
  **改了 `webreport/static/` 要重新 `go build`**（`//go:embed`）。
- **「实时」只有一条链路**：写盘 → 指纹变 → 重扫 → 轮询，别加事件推送或让页面直读抓取进程内存。
  **缓存指纹 = 名字 + 大小 + 修改时刻**，别只留大小，也**别算目录自己的 mtime**。
- **`web/dist` 不是 embed 的**（运行期从磁盘读，发布要连 `dist` 一起带走）；**URL 路径用 `path`、磁盘用
  `filepath`**（`filepath.Clean` 会把 `/assets/x.js` 变成 `\assets\x.js`）。
- **存档是快照不是日志**：可加「属于实例」的字段（`omitempty` 让老档照读），别加「属于进程」的。
- **前端**：`types/protocol.ts` 与 `replay/protocol.go` 逐字对应（对不上不报错，只表现成「面板一直空」）；
  `@` 别名在 `vite.config.ts` 与 `tsconfig.app.json` 两处同步；**方向色只有一个语义**（绿=涨/多/盈，
  红=跌/空/亏），只在 `--c-up`/`--c-down` 与 `CHART_THEME` 两处。**K 线例外**：蜡烛保持黑白。

---

# 二、Spring 侧：服务端（当前实现）

`./mvnw spring-boot:run`（Windows 用 `mvnw.cmd`），端口 **8791**、只绑本机。首次启动自动建
`./db/tradingview-j.db`，Swagger 在 `/swagger-ui.html`（springdoc-openapi 2.8.x，配 Boot 3.4/3.5）。
版本号只有一处：`pom.xml` 的 `<version>` → 资源过滤 → `app.version` → `AppProperties`。
前端在 `webapp/`（Vue 3.5 + Pinia + vue-router + Tailwind 4，端口 5174）。

## 接口

- **PF 模板**：`GET|POST /api/pf-templates`、`GET|PUT|DELETE /api/pf-templates/{id}`。建/改共用
  `PropFirmTemplateRequest`；`PUT` 忽略 `stage`（阶段在链上，改不了）。实体抛的
  `IllegalArgumentException` 由 `GlobalExceptionHandler` 转 400 + 原始中文原因，别让它掉进 500。
- **PF 账户**：`GET /api/pf-accounts?status=ACTIVE|FAILED`、`GET|PUT|DELETE /api/pf-accounts/{id}`、
  `POST /api/pf-accounts`（`templateId` / `name` / **`symbolId` 三个必填**，规则此刻复制进账户、标的
  此刻定死）、`POST /{id}/promote`、`POST /{id}/fail`、`POST /{id}/cursor`（`barTime`，把「盘面最新
  时间」往后推一格，**只能往前**）、`GET /{id}/stages`。
  **没有「改余额」和「重置」**：余额只能由成交重算后经 `syncFromSession` 追平，手改会让账和流水对不上。
  **没有「换标的」**：`PUT` 只读 `name`，请求体里的 `symbolId` 被忽略 —— 换标的等于换一条时间轴，那是
  重开账户。`promote` 在 PAYOUT 上会被拒（否则余额会被再重置一次，前两阶段的账就擦掉了）。交易权限守卫
  在 `PropFirmAccountService#requireSymbol` / `checkPositionLimit`，以后接下单时每处入口都要过一遍。
- **行情（只读，无写入口）**：`GET /api/bars/symbols` 返回清单里的标的 + 每个标的磁盘上日文件个数与
  首尾日期；`GET /api/bars?code=CME_MINI:MES1!&time=2024-07-08 09:00` 返回**上一个交易日整天 + 截止
  那天到 `time`** 的 5 分钟 K 线（上一交易日按 `data/` 里的 CSV 算，不是日历减一天；`time` 不传 =
  最新一天整天；休市日返回空数组不是错误）。数据全来自 `CsvBarStore` + `SymbolCatalog`，**不落库不缓存**；
  `code → folder`、「上一个交易日是哪个文件」、「默认最新一天」只在 `BarQueryService` 一处。webapp 的
  交易页就靠这两个接口画图 —— 这也是那边不用 WebSocket 的原因。
- **Swagger 两个坑**（`OpenApiDocsTest` 钉住）：`UNWRAPPED_PACKAGES` 必须含 `org.springdoc`，否则文档
  端点也被包成 `{code,msg,data}`、页面**白屏**；响应结构要补外壳（springdoc 只看方法签名），由
  `OpenApiConfig` 做。

## 分层与数据模型

`controller → service → repository → SQLite(./db/)`，旁支 `service → storage → data/<folder>/*.csv`。
`dto` 网线形状 · `entity` 按聚合分子包 · `repository` 每聚合根一个接口、不写业务 · `service` 是事务边界
与领域计算（`TradeLedger` 是**唯一**算盈亏的地方）。**K 线不进库**：一天一个文件、能 diff、Go 侧已在写。

分三层看：**日志层（唯一写入点）→ 快照层（当前值，查得快）→ 投影层（日志的一段区间）**。判断「能不能删」
只用一条标准：这个数据是日志，还是能从日志重放出来的当前值。**PF 账户是记账数据的唯一聚合根。**

| 概念 | 关键设计 |
|---|---|
| 标的 | 主键就是代码；`folder` 是 CSV 目录锚；只存 tick/tickValue/commission |
| PF 模板 / 账户 | 模板用 `stage`+`nextTemplateId` 串三阶段链；账户带 `stage`/`baseBalance`/`eodPeakBalance`，规则是建户那刻的副本；绑定标的 `symbolId`（建户时定、`updatable=false`、无 setter）、起点 `startBarTime`（唯一键）+ `startDayFile`、盘面最新时间 `lastBarTime` |
| 绘图 / 下单 | 样式是画下那刻的**快照**；`Drawing` **不带 `account_id`**（挂在 K 线上）；没有止损/止盈单类型，`ocoGroup` 管同根不双触发 |
| 交易 | **账务的唯一真相**，持仓与盈亏由它重放；`commission` 是成交那刻的快照；`Position`/`ClosedLot` **不落库**（均价法给账户，FIFO 给画线） |
| **操作记录** `AuditEvent` | **日志层、唯一写入点**。append-only；每行带 `symbol_id` + `bar_time`；**没有任何下标列** |
| 录音 | 每根最多 9 段；**不带 `account_id`**（挂在 K 线上） |
| 策略 / 机器人 | 算法必须是 Java 类 |
| 交易记录 `TradeRecord` | **日志的区间投影**：`start_seq`~`end_seq` 指向 `audit_event`，外加独立摘要（盈亏/MFE/MAE/标签）和 `accountName` 快照，不复制流水 |

**删账户不删数据**：原「会话」概念整个不存在了，归属改成 `account_id`，这一列 **`NOT NULL` 但不加外键**
—— 删账户只删账户行，其余五张表原样保留（`SET NULL` 抹归属、`CASCADE` 连带删、`RESTRICT` 让账户永远
删不掉，三种级联都不合适）。规则落在 Java 侧：工厂方法拿不到账户 id 直接抛 `IllegalArgumentException`，
`#requireActive/requireTradable` 再拦一笔。**关系一律按 id 引用，不写 `@ManyToOne`**；真需库守的外键才写
`REFERENCES`。

**图形和语音只有 `symbol_id`、没有 `account_id`**：它们依附 K 线，不参加任何账户统计（余额/回撤/一致性/
盈利日都不看它们），所以 `Drawing.of(...)` / `AudioClip.record(...)` 压根没有账户参数，删账户天然不影响
它们。挂 K 线的表各带 `symbol_id` 是「这条事实发生在哪块盘面上」的留痕。

**复盘进度不单独存**（`ReplayCursor` 已删）：进度 = 「该(账户,品种)最后一条 `audit_event` 的 `bar_time`」。
原来「唯一算不出来、必须存」的理由，只在「没有全量操作记录」时才成立；两处存同一份状态早晚对不上。

**全库没有「K 线下标」字段**：`audit_event.cursor`、`trade_order.placed_index/filled_index`、
`prop_firm_account.start_bar_index` 全删。理由同一条：**下标是位置，时间是标识** —— 文件重抓 / 补一根 /
删一根坏行，同一个下标就指向另一根且不报错；时间永远指向原来那颗。保留的是**序号**（`trade.seq` /
`audit_event.seq` / `prop_firm_stage_log.seq` / `audio_clip.seq`，定同一根内多件事的先后，推不出来）和
`Anchor.offset`（「向右延伸 40 根」是用户意图，不是某个下标的值）。**别加回来。**

**不做撤销**：append-only 的日志里删事件就不叫「记录在案」；交易类动作（平仓）不可逆，撤销等于改账。
盘面类若将来要撤，只能追加补偿事件。已知边界：图形/语音不版本化（`moveTo` 覆盖端点、删除即删行），所以
「全局查看」还原的是盘面**现在**的样子，不是平仓那一刻。

## 起点与盘面位置

**起点 = 开账户时在绑定标的里随机抽一根 K 线**：`PropFirmStartPicker` 取该标的（`symbol.folder`）的
**全部日文件**，随机挑一份，定位到**当天 09:00 及之后的第一根**。由 `PropFirmAccountService#open` 调用，
结果写进 `start_bar_time` / `start_day_file`。

- **只在这个标的的目录里抽**：起点必须真实存在于账户自己的图上，否则日历首月、可交易日下界、回放起点
  全建立在一个不存在的坐标上。
- **抽的是「日文件」不是「日期」**：文件自带存在性，写进库的那根一定读得出来；「先抽日期再查有没有这根」
  要另外处理两种空结果。
- **顺延而不是报错**：真实数据里有些日子不是从 07:00 开始的，要求「正好有一根 09:00」会让开账户随日期
  **随机失败**（一周撞一次，最难查）；整天都在 09:00 之前就换下一份文件。
- **抽不到就不建账户**：先抽再落库，抽失败库里零残留。标的不在 `symbol` 表里也在这步拒掉。
- **起点只有一个键**：`startBarTime`（`startBarIndex` 已删）。**`started_at` ≠ `start_bar_time`**：前者是
  真实时钟（这行何时写进库），后者是行情时间轴上的位置。起点字段全 `updatable = false` —— 改一次，这个
  账户的所有历史都跟着挪位置。

**`last_bar_time`（盘面停在哪一根）**：建户时 = 起点，用户每在交易页步进一根就往后推一格
（`#advanceBarTime`，**只增不减**，重复提交同一根幂等，格式必须 `yyyy-MM-dd HH:mm`）。**为什么要有**：
「继续交易」打开交易页时只揭示到这一根；没有它每次回来都整天全揭示 = 提前看到未来，训练失效。**为什么
不能退**：账按时间累积，退回去就得回滚账，那是另一件事。**为什么不随升级重置**：EVAL→FUNDED 只是换规则，
盘面还停在那一根。旧库补列（`ddl-auto: none`）：
`ALTER TABLE prop_firm_account ADD COLUMN last_bar_time TEXT NOT NULL DEFAULT '';` 再
`UPDATE prop_firm_account SET last_bar_time = start_bar_time WHERE last_bar_time = '';`

## PF 考核规则

**三阶段挤在同一行账户上**（`EVAL → FUNDED → PAYOUT`，单向），历史在 `prop_firm_stage_log`
（`reason` = 怎么来的 / `end_reason` = 怎么结束的）。升级时余额被强制拨回账户规模，所以**余额不能由成交
反推**，每阶段各存 `base_balance`。

- **交易日 = `[D 06:00, 次日 04:45)`**，04:45–06:00 不许交易、也不属于任何交易日；不做夏冬令时（CSV 明文
  当字符串比），`PropFirmClock` 是唯一实现。
- **爆仓逐根判定**（`PropFirmSession`）：权益 = 余额 + 全部浮盈亏，浮亏用本根**最不利价**（多单看 low、
  空单看 high）—— 用收盘价会漏判，一根长下影可以盘中穿线再收回来。**等于亏损线也算失败**。亏损线只在
  **日终**上移（盘中赚到新高不算），峰值达到 `trailing_lock_level` 后冻结。
- **只留一致性 50%**：`目标 = max(模板目标, 单日最大净盈利 / 0.5)`（总盈利 3000、单日 1800 → 目标抬到
  3600，所以 3000 不能通关）。
- **手数分档只属于 FUNDED**：按利润 0→20 / 1000→30 / 2000→40，判每品种净持仓；EVAL 是**固定上限 40**，
  赚多少都不放大。`PropFirmPositionLimit` 按 `stage` 分叉，非 FUNDED 只认「起点最小那档」；
  `PropFirmTemplate.checkTiers` 禁止 EVAL 配多档 / 起点≠0。
- **PAYOUT 只是「不能再下单」**，账户仍 ACTIVE（还能画图录音）→ `Status` 只有 **ACTIVE / FAILED**，别再立
  COMPLETED。
- **三套时间口径别混**：23:55 日历日强平（反向市价平 + 撤该品种挂单，不留隔夜）；04:45–06:00 禁交易窗
  （这些 K 线整个跳过）；04:50 交易日结算收尾（跳过但要触发日终结算）。

## 标的清单与手续费

`SymbolSyncService`（`ApplicationRunner`）每次启动读 `<bar-root>/_symbols.json`，按代码 upsert
`folder/tick/tickValue/commission`。**内容没变的行不写**（否则每次启动 `updated_at` 全被刷新，日志里
「更新 4 条」会把真改了费率的那个淹掉）。幂等，改费率重启即生效。库里有、清单里没有的**只 warn 不删**
（可能是清单被临时覆盖，删了已有成交引用会落空）。四种情况分开处理：清单不存在 = 空转；清单坏了 = 启动
日志 ERROR 但不拦启动；缺 `commission` 的标的跳过；`folder` 撞 UNIQUE = 跳过该条、不带走整张表。

**为什么必须有这一步**：`PropFirmStartPicker` 只按 `symbol.folder` 找 CSV，**从不枚举 `data/` 根目录**。
表空着时 `data/` 下有几万个 csv 也一个都看不见 —— 这就是 2026-10-30「明明有数据却报 `data/ 下没有 .csv`」
的成因（当时报错文案和真实原因完全对不上，已拆成两条）。

**手续费 = 标的自己的参数，没有价目表**：`symbol.commission` 一列（**每手每边** USD），**`NOT NULL`**，
`0` = 明确免费、`>0` = 收费，**没有「还没配」这个第三态**。`Symbol#of(code, folder, commission)` 与
`setCommission` 都收不了 `null`，所以 `perSideCommission` / `roundTripCommission` 不再返回 `null`；
「往返乘不乘 2」只有 `Symbol#roundTripCommission` 一处回答。
Go 侧会**整体重写** `_symbols.json`，0.31.0 起 `commission` 原样带回去（`*float64`，`0` 不被 `omitempty`
省掉）。**教训**：`saveXxxLocked` 会整体重写的文件，用户手写的每个键都必须有对应 struct 字段。⚠️ Go 侧仍
可能写出「没有 `commission` 键」的标的（新标的一出现抓取端不知道费率），这种行**进不了库**，必须先在
`_symbols.json` 里补上费率才谈得上建标。

## 全局约定

1. **金额一律 `BigDecimal`、列是 TEXT**：SQLite 没 decimal，`DECIMAL` 会被 NUMERIC 亲和性转成 REAL 丢精度。
2. **时间价格用「明文 / 毫秒整数」**：K 线时间是 CSV 原文，转成时间戳就对不上协议。
3. **能算的不存**（持仓、盈亏）· **快照优于引用**（样式、规则从模板复制）· **主键是 UUID**（`Symbol` 除外）。
4. **成交先后只认 `trade.seq`**（同一账户内从 1 单调递增），**不认主键**：id 是 UUID，同一根上多笔成交按
   它排等于随机排，逐批归因会随读库顺序变。排序统一 `Trade.executionOrder()`，仓储方法一律
   `...OrderByBarTimeAscSeqAsc`；`seq` 由写入方在同一事务里取 `max(seq)+1`。
5. **余额只能由 `syncComputed` 追平重算结果**，没有增量记账入口（旧的 `applyNetPnl` 已删）：两条算路必然
   分叉，增量那条容易漏掉开仓那笔的手续费。
6. **枚举**一律 `@Enumerated(EnumType.STRING)` 存常量名；Go 要小写串，换算只在输出侧做；`@Enumerated` 与
   `@Convert` 不能同字段；新增动作不能撞小写名。

⚠️ `Trade.realizedPnl`（落库）与 `TradeLedger` 现算 realized 是**两处计算**，将来要合并。

## 测试 · 改表

`./mvnw test`。覆盖：`PropFirmAccountServiceTest`（建户复制规则快照 / 起点抽不到就不建 / 等级迁移结流水 /
删账户不连坐 / 标的是真外键必填且升级不改它 / 别的标的被 `requireSymbol` 与手数闸门拒掉）·
`PropFirmStartPickerTest`（09:00 定位 / 顺延 / 跳过不合格日 / 没数据就报错 / 只在自己标的目录里抽）·
`PropFirmAccountControllerTest`（JSON 契约含 `symbolId`）· `PropFirmTemplate*Test` · `SymbolSyncServiceTest` ·
`SymbolCatalogTest` · `CsvBarStoreTest` · `BarQueryServiceTest`（只读，扫描前后目录快照一致）。
两个 controller 测试是 `@WebMvcTest`（不起 JPA、不碰库）；三支 `@SpringBootTest` 用
`@DynamicPropertySource` 把库指到临时目录，跑测试不会改用户账户数据。

`ddl-auto` 是 `none`，表结构全由 `schema.sql` 管（SQLite 的 `ALTER TABLE` 支持极有限，Hibernate 的 `update`
会生成它不支持的 DDL，且只在「第二次启动且字段变了」时失败）。**改表结构必须重建 `./db/`**：`schema.sql`
全是 `CREATE IF NOT EXISTS`，对旧表什么都不做，启动时报 `no such column`。没有迁移脚本，挪走旧库即可：
`mv db/tradingview-j.db db/tradingview-j.db.bak-$(date +%Y%m%d)-<原因>`（**别删**）。反过来**删表
`schema.sql` 永远做不到**，必须手 `DROP`。⚠️ 加 NOT NULL 列**不会**让 controller 测试变红（它们不往这两张
表插数据），所以「测试全绿」不等于「旧库能用」；改完表结构要自己 `pragma table_info` 看一眼真实列。
加列的三次现场都在 `db/*.bak-*` 里（`audit_event-range` / `account-start-and-name` / `account-symbol`）。

---

# 三、交易引擎（设计，未接通前端）

`webapp` 的下单 / 撤单 / 平仓目前一律走 `notWired()` 只给提示，**不造本地成交**（账在后端算，前端造假会
立刻对不上）。下面是接通时的既定方案；这几条判据已经定了，动手前先读。

## 撮合

| 类型 | 方向 | 触发 | 成交价 | 例（挂 100） |
|---|---|---|---|---|
| LIMIT | BUY | `low ≤ p` | `min(p, open)` | open=98 → **98**（低开更优） |
| LIMIT | SELL | `high ≥ p` | `max(p, open)` | open=103 → **103** |
| STOP | BUY | `high ≥ p` | `max(p, open)` | open=106 → **106**（不白捡） |
| STOP | SELL | `low ≤ p` | `min(p, open)` | open=94 → **94** |

**跳空修正不能省**：不加的话每次跳空成交都白送钱，回测数字全废。其余规则：

- **本根挂的单本根不撮合**，从下一根起 —— 需要列 `trade_order.effective_from_bar`（K 线时间明文，**不是
  下标**）。**别用 `placedAtBar` 推**：改价之后这张单在时间上已是「新单」，按原挂单根算等于用改价前的那根
  K 线撮合改价后的价格，这是一条真实的漏账路径。
- **OCO 组一根里最多成交一张**，其余撤销。5 分钟 K 线只有 OHLC，分不出 high/low 谁先到，**组内止损优先**
  （取对持仓不利那侧）：这系统用来训练，宽松假设会让人在真实市场里第一次见「止损先到」时毫无准备。
- **不做**部分成交、滑点、盘口深度、盘前盘后；一单只成交一次。
- **确定性是硬要求**：同一组输入必须输出连顺序都相同的成交列表，否则同一个复盘动作跑两遍得到两个余额，
  用户会立刻不信任这个系统。

## 编排 vs 通知（本方案的第一性约束）

> **这个动作失败，事实表该不该回滚？** 该 → **编排**（同步调用、同一事务、有返回值、有顺序）；
> 不该 → **通知**（`@TransactionalEventListener(AFTER_COMMIT)`，无顺序，失败只记日志）。

| 环节 | 归类 |
|---|---|
| 撮合 → 生成成交、`seq` 分配、考核判定、写 `audit_event` | **编排** |
| 交易记录摘要（MFE/MAE/标签）、界面状态、阶段统计汇总列 | **通知** |

所以**撮合绝不能做成事件**：`撮合 → 成交落库 → 重放账本 → 判权益 → 可能 FAILED → 未成交挂单作废` 有严格
先后依赖，且用户在同一次 `advance` 的响应里就要看到「距爆仓还有 $482」，异步拿不到。**也不上消息中间件**：
判据是「消费者是否在另一个 JVM / 另一台机器」，当前全在同一进程。

**这是事件驱动，不是事件溯源**：状态在表里（`trade_order.status` / `trade` 行），事件只是**变更通知** ——
可丢、可重投、可重建；不要从 `audit_event` 重放挂单和持仓，也不要把 `trade` 降级成投影。**领域事件与
`audit_event` 是两条流**（系统视角「发生了什么」vs 用户视角「这一步在盘面上长什么样」）：事务内写
`audit_event`（承载 `TradeRecord` 的区间语义），提交后发领域事件。**不许让投影去消费 `audit_event`**
（要反序列化 `payload_json`，又慢又脆）。投影必须幂等 + 能全量重建，「事件丢了也没关系」才不是空话 ——
丢了的最坏后果只是交易记录 MFE/MAE 暂时为空，下次 `finish()` 或重启补上。**投影抛异常必须吞掉**：事务已
提交，往上抛会让用户看到「失败」而实际成交已成立，他会重复下单。

## 订单状态机与并发

`place() → OPEN`，`markFilled()` / `cancel()` → 终态，**只有三个状态**。不加 `PENDING / SUBMITTED /
PARTIALLY_FILLED / REJECTED`：没有券商没有网络确认，`SUBMITTED` 是没人能观测到的状态；部分成交不做；拒绝
是命令的返回值，订单从没存在过，哪来的状态。**「为什么撤的」用字段不用状态**：
`trade_order.cancel_reason`（可空，仅 CANCELLED 时有值）`USER | OCO_MUTEX | CLOSE_OUT |
ACCOUNT_FAILED | MANUAL_FLAT`。状态一多 `switch` 会指数膨胀，每个新状态都要重新回答「它能不能成交」。

- **按账户加锁**（`AccountLocks`，`ReentrantLock`），**不用 `@Version` 乐观锁**：撮合是「读一批单 → 算 →
  改一批单」，乐观锁重试时看到的订单集合已经变了，两次结果不一致，撮合就变成不确定的。粒度是**账户不是
  品种**（权益跨品种合并，两品种并发会让 `onBar` 顺序不定，从而影响「同一时刻哪笔成交先应用」）。
- **幂等推进**：请求带 `expectedBarTime`，与游标（最后一条 `audit_event.bar_time`）不符就
  `409 CURSOR_STALE` + 当前值，前端刷新状态而非重推。比请求去重表轻。
- **平仓三种入口（主动 / 挂单触发 / 系统强平）一个出口**：全部收敛成「一笔反向 `Trade`」，不加「平仓」
  交易类型、不加 `isClose`（加了就多一个能和净持仓符号不一致的字段）。两条必校验：`qty ≤ |净持仓|`
  （超出 **400 拒绝**，不许自动变成「平 2 手 + 开 1 手反向仓」—— 反手是另一种意图），且平仓前重新读净持仓
  （持仓是重放结果，不是存的状态）。**强平先平后撤**：倒过来一旦平仓失败就变成「挂单没了、仓还在」。
- **手数上限 = 净持仓 + 未成交挂单**：只算净持仓的话，用户可以挂 20 张 20 手限价单在下一根全触发。

## 边界（挑最容易被搞错的）

- 权益 **== 亏损线判 FAILED**（等于即失败）。
- 23:55 那根同时有挂单触发 → **先撮合、后强平**（强平是收盘动作，价格已经走完；反过来会用收盘价把本该
  触发的挂单抹掉）。
- 04:45–06:00 那 12 根 → 不撮合、不动游标、返回 `SKIPPED`；挂单保持 OPEN 但不会被它们触发。
- 用户停在最后一根没调 `finish` → 最后一个交易日不结算、峰值不更新、盈利日不计数、EVAL 不升级。
  **前端「退出」必须调 `finish`**。
- 账户已 FAILED 还被推进 → 直接返回，不撮合、不产生成交；剩余 OPEN 单在失败那一刻已作废。
- CSV 被 Excel 占用 → 整根回滚、返回可重试错误码；有坏行 → warn 并跳过（缺口不报警是已知取舍）。
- 账户被删 → 挂单/成交/事件保留 `account_id`（不加外键），`TradeRecord` 靠 `accountName` 快照仍可读。
- **交易记录的开/收**：空仓 → 有仓开始，回到空仓收尾；锚点是「开仓成交**之前**那一根」（第一屏应当是
  「还没进场」的样子）。MFE/MAE 在收尾时全量重放，**排除有成交的那几根**（一根只有 OHLC，有成交那根的
  高低点等于替交易者挑时机）。

**五个不变量（写进测试，不是写进注释）**：① `Σ trade.realizedPnl` = `TradeLedger.replay(全部成交)`；
② `order.status == FILLED` ⟺ 存在 `trade.order_id` 指向它；③ `seq` 在同一账户内从 1 连续无跳号；
④ `balance = base_balance + Σ trade.netPnl()`（本阶段）；⑤ 同一根上 `trade` 的顺序 = `executionOrder()`。

---

## 注意

抓取属于对 TradingView 服务条款的灰色操作：低频小批量自用可控，**不要高频批量拉、不要全品种刷**，也别
长时间挂着连录几十小时。仓库未声明开源许可。

> 旧文档 `SERVER.md` / `SERVER-DESIGN.md` / `TRADING-ENGINE-DESIGN.md` 的内容已合并到本文件，原件存档在
> `archive/20261030-design-docs/`（`SERVER.md` 当时有未提交改动，故未直接删除）。
