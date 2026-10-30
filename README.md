# tradingview-j

一个仓库两块，**互不引用**：

- **Go 侧**（`main.go`/`internal/`/`web/`）：旁路抓你**本人已登录的 Chrome** 里 TradingView 图表页的 WS 帧，
  解 5 分钟 K 线落 CSV 并做本地复盘。零第三方依赖，产物 `tv-capture-v<版本>.exe`（`0.31.0`）。只**被动**监听。
- **Spring 侧**（`src/`/`webapp/`）：Go 版复盘系统的迁移目标，SQLite，端口 8791；交易撮合仍是方案。

# 一、Go 侧：抓取 + 复盘

## 跑起来

Chrome 里开 `chrome://inspect/#remote-debugging` → 勾 Allow → 打开带 `/chart/` 的图表页、周期切 5 分钟（Chrome
136+ 加 `--remote-debugging-port` 会被静默忽略，只能走这个开关）。需 Go 1.26+ 与 `go-winres`。

```bat
build.cmd            :: 前端 + exe 一次做完（任一阶段失败即中断）· --no-web 只编 exe
```

版本号只有 `VERSION` 一个来源（文件名 / exe 属性 / 横幅页脚三处），**升版本 = 改它再重跑 `build.cmd`**；
`.syso` 是编译期快照，别手改别提交。`web/dist` **不进 exe**，发布要连 `dist` 一起带走。

菜单三项：`1 启动抓取（录 CSV + 开分析页）` · `2 复盘（回放/标注/模拟交易/交易记录）` · `3 退出`。

**菜单 1**：等调试端口与图表页（没开就每 5s 重试），分析页在 `127.0.0.1:8765`；你只需往回拖图表，程序把帧
录成 CSV，`Ctrl+C` 先刷盘再关页面。分析页对数据目录**只读**（不建目录、不写索引、无写接口），每标的一张
月历显示当天根数、异常日**变淡不隐藏**。实时靠「写盘 → 比对磁盘指纹 → 重扫 → 页面每 2s 轮询」，**无推送**。

**菜单 2**：逐根回放 `data/` 的 K 线，画标注、做模拟交易。**有状态应用，但仍只读数据目录**（状态写 `state/`，
一个 CSV 都不改）：实例锚点与【下一根】、五种标注、挂单 + nATR 定手数、EMA20/ATR14/Doji/ZigZag、观众模式、
回放延伸、交易记录月历。改前端必须 `npm run build`。

## 配置 · 数据 · 结构

`config.json` 在 exe 同目录，**不接受命令行参数**，相对路径相对 exe 目录。常改 `matchUrl`、`symbol`
（留空自动识别）、`intervalSeconds`(300，**只收这个周期**)、`outDir`/`rawDir`、`webPort`/`replayPort`。

```
data/_symbols.json        标的名 → folder / tick / tickValue / commission
data/<folder>/<date>.csv  time,open,high,low,close,volume（时间列明文 yyyy-MM-dd HH:MM、本机时区、开盘时间）
state/ presets/ recordings/ raw/   复盘状态 · 绘图模板 · 录音 · 原始帧（都不入库）
main.go(只有 main+version) · cli(菜单,唯一 stdin reader) · capture(常驻) · cdp · wsclient · tvproto(分帧/抽K线)
ingest(周期反推/落盘) · store(CSV + 只读 scan) · webreport(分析页) · replay(实例/标注/撮合/记录) · strategy
```

分层按「**谁能 import 谁**」；`internal/` 保持扁平，入口不搬 `cmd/`（`.syso` 只链同目录包）；`web/` 用
Vue 3.5 + Pinia + Tailwind 4，K 线 Canvas 自绘、不用图表库。

## 不能让步的约定

- **标的名只认 `symbol_resolved`**，别在整帧里搜 `pro_name`（同一条 WS 上还有自选/报价的 `qsd`，带**别的
  品种**）。顺序：`config.symbol` → 序列绑定 → 会话内唯一 → 页面内唯一 → URL 猜；给不出结论就**先攒不落盘**。
  周期靠**相邻时间戳众数差**反推，按「会话 + 序列」分别投票；`lbs.bar_close_time` 是陷阱（可视边界）。
- **统计口径只有一份**（`store.Scan`）：根数/断档/应有根数/异常判定 —— 在 JS 里重算会出现两份口径，而**同一
  目录给出不同根数时用户没法裁决**。**「分析」不许写任何东西**，复盘也只用 `ReadDayRows`/`Catalog`。
- **前端预览、后端定稿**：除缩放/平移/十字光标没有操作在本地改状态，正式状态永远来自广播；唯一两边各算一遍
  的是 Ctrl 吸附（漂了会「松手后图形跳一下」）。EMA20/ATR14 后端算（**ATR 钉在 14**），Doji/ZigZag 留前端。
- **撮合规则只有一份**（`instance.go`），机器人走同一套；本根挂的单从**下一根**才撮合。**前端不算钱**，
  唯一例外是 nATR 定手数（每点价值必须 = `tickValue / tick`）；一键退仓**先平后撤**。
- **一条链路 + 一个写入口**：实时只有「写盘 → 指纹（名字+大小+修改时刻）→ 重扫 → 轮询」，别加推送；CSV 行
  格式只在 `store.readDayFile` 定义一次；网页版不许有算数字的代码（改格式同步 `format.test.cjs`）；存档是
  **快照不是日志**。
- 方向色只有一个语义（绿=涨/多/盈，红=跌/空/亏），只在 `--c-up`/`--c-down` 与 `CHART_THEME` 两处；**K 线蜡烛保持黑白**。

`go test ./...`（全离线）· `cd web && npm run type-check && npm run lint && npm run build`；前端改完要**用浏览器
真跑一遍**再看结论。抓取侧用假 CDP 服务端（`internal/wstest`）喂 `testdata/` 的真实抓包。**`build.cmd` 必须纯
ASCII + CRLF**、**菜单只能有一个 stdin reader**（都有测试拦）。

# 二、Spring 侧：服务端（当前实现）

`./mvnw spring-boot:run`，端口 **8791**、只绑本机；首次启动建 `./db/tradingview-j.db`，Swagger 在
`/swagger-ui.html`（springdoc 2.8.x）。版本号只一处：`pom.xml` 的 `<version>`。

**接口**

- **PF 模板**：`/api/pf-templates` 列表+增、`/{id}` 查改删；`PUT` 忽略 `stage`（阶段在链上）。
- **PF 账户**：列表（`?status=`）、增（`templateId`/`name`/**`symbolId` 三必填**）、查改删、`promote`、
  `fail`、`cursor`（推 `lastBarTime`，**只往前**）、`stages`。**没有改余额 / 重置 / 换标的**——余额只能由
  成交重算后追平；换标的等于换一条时间轴，那是重开账户。
- **行情（只读）**：`/api/bars/symbols`、`/api/bars?code=&time=`（上一交易日整天 + 截止 `time`，上一交易日
  按磁盘 CSV 算不是日历减一；**不落库不缓存**）· 实体抛的 `IllegalArgumentException` 转 400 · Swagger 的
  `UNWRAPPED_PACKAGES` 必须含 `org.springdoc`，否则文档页**白屏**。

**数据模型**：**PF 账户是记账数据的唯一聚合根**，K 线不进库。三层 = **日志层 `audit_event`（唯一写入点、
append-only、每行带 `symbol_id` + `bar_time`）→ 快照层（当前值）→ 投影层 `TradeRecord`（`start_seq`~
`end_seq` 区间 + 独立摘要）**。`Trade` 是账务唯一真相（持仓/盈亏由它重放），`trade.seq` 定同一根内先后；
`Drawing`/`AudioClip` 只挂 K 线、**不带 `account_id`**，不参与账户统计。

- **删账户不删数据**：子表 `account_id` **NOT NULL 但不加外键**（SET NULL 抹归属、CASCADE 连带删、
  RESTRICT 让账户永远删不掉），闸门在实体工厂 + `requireActive`/`requireTradable`。
- **进度不单独存**：= 该(账户,品种)最后一条 `audit_event.bar_time`。**不做撤销**（append-only 只能补偿）。
- **全库没有「K 线下标」字段**：**下标是位置，时间是标识** —— 重抓/补一根/删坏行会让下标悄悄指向另一根
  且不报错。保留的只有序号 `*.seq` 和 `Anchor.offset`（「延伸 40 根」是意图）。**别加回来。**
- **账户绑定唯一标的**：`symbolId` 建户必填、无 setter；起点 `startBarTime` + `startDayFile`，建户时在**该标
  的目录**里随机抽一份日文件、取当天 **09:00 及之后第一根**（抽不到就不建账户）；`lastBarTime` = 盘面停在哪
  一根，**只增不减**（否则每次打开都全揭示 = 提前看到未来）。

**PF 考核**：三阶段 `EVAL → FUNDED → PAYOUT` 挤在同一行账户上，历史在 `prop_firm_stage_log`；升级时余额被
拨回账户规模，所以**余额不能由成交反推**，每阶段各存 `base_balance`。

- **交易日 `[D 06:00, 次日 04:45)`**，04:45–06:00 禁交易；23:55 日历日强平、04:50 结算收尾。不做夏冬令时，
  `PropFirmClock` 是唯一实现。**逐根判爆仓**：浮亏用本根极值（多单 low / 空单 high），**等于也算失败**，
  亏损线只在日终上移。**一致性 50%**：`目标 = max(模板目标, 单日最大净盈利 / 0.5)`。
- **手数分档只属于 FUNDED**（利润 0→20 / 1000→30 / 2000→40），EVAL 固定上限 40。`PAYOUT` 只是「不能再
  下单」，账户仍 ACTIVE → `Status` 只有 ACTIVE / FAILED。

**约定**：金额一律 `BigDecimal`（列 TEXT）· 时间价格用明文/毫秒整数 · **能算的不存** · 快照优于引用 ·
主键 UUID · **余额只能由 `syncComputed` 追平**（旧的 `applyNetPnl` 已删，增量那条会漏开仓手续费）· 枚举存常量名。

**手续费**：`symbol.commission`（**每手每边**，**NOT NULL**：`0` 免费 / `>0` 收费，没有「没配」第三态），
成交那刻快照进 `trade`；「往返乘 2」只有 `roundTripCommission` 一处回答。`_symbols.json` 启动时由
`SymbolSyncService` upsert（内容没变不写，库里有清单里没有只 warn 不删）。⚠️ Go 侧会**整体重写**该文件，
**用户手写的每个键都必须有对应 struct 字段**，缺 `commission` 的标的进不了库。

**改表必须重建 `./db/`**（`ddl-auto: none`；`schema.sql` 全是 CREATE IF NOT EXISTS，删表只能手 `DROP`），
旧库改名 `db/*.bak-日期-原因` **别删**。「测试全绿」不等于「旧库能用」。

# 三、交易引擎（设计，未接通前端）

`webapp` 的下单/撤单/平仓一律只给提示、**不造本地成交**（账在后端算，前端造假会立刻对不上）。

| 类型 | 触发 | 成交价 |
|---|---|---|
| LIMIT BUY / SELL | `low ≤ p` / `high ≥ p` | `min(p, open)` / `max(p, open)` |
| STOP BUY / SELL | `high ≥ p` / `low ≤ p` | `max(p, open)` / `min(p, open)` |

**跳空修正不能省**（不加则每次跳空都白送钱、回测数字全废）。**本根挂的单本根不撮合**（列
`effective_from_bar`，**别用 `placedAtBar` 推**）。**OCO 一根只成交一张**，分不清高低谁先到时**取对持仓不
利那侧**（组内止损优先）。不做部分成交/滑点。

> **这个动作失败，事实表该不该回滚？** 该 → **编排**（同步、同事务、有返回值、有顺序）；
> 不该 → **通知**（`AFTER_COMMIT`，失败只记日志）。

撮合、落成交、`seq` 分配、考核判定、写 `audit_event` 全是**编排** —— **撮合绝不能做成事件**（有严格先后
依赖，结果要同步返回）。事件驱动 ≠ 事件溯源：状态在表里，事件只是变更通知，可丢可重建；**领域事件与
`audit_event` 是两条流**，别让投影去消费 `audit_event`。不上消息中间件（消费者都在同一进程）。

订单只有 `OPEN → FILLED | CANCELLED`，撤单原因放 `cancel_reason` 字段而不是新状态。**按账户加锁，不用
乐观锁**（撮合是「读一批→算→改一批」，重试时订单集合已经变了）。**幂等**靠 `expectedBarTime` 比对游标，
不符回 409。平仓三入口一个出口（一笔反向 `Trade`），`qty ≤ |净持仓|` 否则 400；**强平先平后撤**；手数
上限算「净持仓 + 未成交挂单」。

**边界**：权益 **==** 亏损线判 FAILED · 23:55 那根**先撮合后强平** · 04:45–06:00 的 12 根跳过 · 用户停在
最后一根必须调 `finish`（否则最后一天不结算、不升级）· CSV 被 Excel 占用整根回滚。

# 注意

抓取属于对 TradingView 服务条款的灰色操作：低频小批量自用可控，**不要高频批量拉、不要全品种刷、别连录
几十小时**。仓库未声明开源许可。更细的版本（三份旧文档合并稿：完整接口表、事件定义、实现顺序、全部边界
场景、Java 伪代码）在 `archive/20261030-design-docs/`。
