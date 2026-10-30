# tradingview-j-server

Go 版 `tradingview-j` 的 Java / Spring Boot 迁移目标，工程文件（`pom.xml` / `src/` / `mvnw`）在**仓库根**，
与 Go 模块共用一个根、**互不引用**。已有版本号 + PF 模板接口和领域层与 SQLite 持久化；复盘/抓取的接口还没搬。

## 跑

`./mvnw spring-boot:run`（Windows 用 `mvnw.cmd`）。端口 **8791**、只绑本机。
`GET /api/version` → `{"code":0,"msg":"ok","data":{...}}`；Swagger 在 `/swagger-ui.html`，用
**springdoc-openapi 2.8.x**（配 Boot 3.4/3.5；`3.x` 给 Boot 4）。首次启动自动建 `./db/tradingview-j.db`。

PF 模板的五个入口（前端 `webapp/` 就在用这几个）：`GET|POST /api/pf-templates`、
`GET|PUT|DELETE /api/pf-templates/{id}`。建/改共用 `PropFirmTemplateRequest`；`PUT` 忽略 `stage`
（阶段在链上，改不了）；实体抛的 `IllegalArgumentException` 由 `GlobalExceptionHandler` 转成
400 + 原始中文原因，别再让它掉进 500。

PF 账户的入口（`PropFirmAccountController`）：`GET /api/pf-accounts?status=ACTIVE|FAILED`、
`GET|PUT|DELETE /api/pf-accounts/{id}`、`POST /api/pf-accounts`（`PropFirmAccountRequest`：
`templateId` 必填、规则此刻复制进账户）、`POST /{id}/promote`、`POST /{id}/fail`、
`GET /{id}/stages`。**没有「改余额」和「重置」接口**：余额只能由成交重算后经
`syncFromSession` 追平，手改会让账和成交流水对不上。`promote` 在 PAYOUT 上会被拒（否则余额会被
再重置一次，前两个阶段的账就擦掉了）。

**Swagger 两个坑**（`OpenApiDocsTest` 钉住）：`UNWRAPPED_PACKAGES` 必须含 `org.springdoc`，否则文档端点也被
包成 `{code,msg,data}`、页面**白屏**；响应结构也要补外壳（springdoc 只看方法签名），由 `OpenApiConfig` 做。

版本号只有一处：`pom.xml` 的 `<version>` → 资源过滤 → `app.version` → `AppProperties`。

## 分层

`controller → service → repository → SQLite(./db/)`，旁支 `service → storage → data/<folder>/*.csv`。
`dto` 网线形状 · `entity` 按聚合分子包 · `repository` 每聚合根一个接口、不写业务 · `service` 是事务边界与领域
计算（`TradeLedger` 是**唯一**算盈亏的地方）。K 线不进库：一天一个文件、能 diff、Go 侧已在写。

## 数据模型（11 个业务概念，PF 账户是记账数据的唯一聚合根）

分三层看：**日志层（唯一写入点）→ 快照层（当前值，为了查得快）→ 投影层（日志的一段区间）**。
判断「能不能删」只用一条标准：这个数据是日志，还是能从日志重放出来的当前值。

| 概念 | 实体（包） | 关键设计 |
|---|---|---|
| 标的 | `Symbol`（market） | 主键就是代码；`folder` 是 CSV 目录锚；tick/tickValue/commission 只存这 |
| PF 模板 / 账户 | `PropFirmTemplate` / `PropFirmAccount`（propfirm） | 模板 `stage`+`nextTemplateId` 串成三阶段链；账户是**唯一归属点**，带 `stage`/`baseBalance`/`eodPeakBalance`，规则是建户那刻的副本；另有**起点** `startBarTime`（唯一键）+ `startDayFile` |
| 绘图 / 下单 | `Drawing` / `Order` | 样式是**画下那刻的快照**；`Drawing` **不带 `account_id`**（挂在 K 线上）；没有止损/止盈单类型，`ocoGroup` 管同根不双触发 |
| 交易 | `Trade`（trade） | **交易域的唯一真相**，持仓与盈亏由它重放；`commission` 是成交那刻的快照；`Position`/`ClosedLot` **不落库**（均价法给账户，FIFO 给画线） |
| **操作记录** | `AuditEvent`（audit） | **日志层，唯一写入点**。append-only；每行带 `symbol_id` + `bar_time`，能定位到「哪块盘面的哪一根」；**没有任何下标列** |
| 录音 | `AudioClip`（voice） | 每根最多 9 段；**不带 `account_id`**（挂在 K 线上） |
| 策略 / 机器人 | `Strategy` / `StrategyBot` | 算法必须是 Java 类 |
| 交易记录 | `TradeRecord`（record） | **日志的一个区间投影**：`start_seq` ~ `end_seq` 指向 `audit_event`，外加一份独立摘要（盈亏/MFE/MAE/标签）和 **`accountName` 快照**。不复制流水 |

**复盘进度不单独存了**（`ReplayCursor` 于 2026-10-29 删除）：进度 = 「这个账户在这个品种上最后一条
最后一条 `audit_event` 的 `bar_time`」。原来的理由是「唯一算不出来、必须存的状态」，那个理由只在
「没有全量操作记录」时才成立；两处存同一份状态，早晚会对不上。

「挂 K 线」的表（drawing / audio_clip / trade / trade_order / trade_record / strategy_bot）各带
`symbol_id` —— **账户不绑单个标的**，一个账户可同时做 MES / MNQ / MGC。

**图形和语音只有 `symbol_id`，没有 `account_id`**：它们依附在 K 线上，不参加任何账户统计
（余额、回撤、一致性、盈利日都不看它们），所以 `Drawing.of(symbolId, kind, start, end, createdBy)` 和
`AudioClip.record(symbolId, barTime, seq, …)` 压根没有账户参数。删账户天然不影响它们，不用靠
「保留原 id」来兜。

**关系一律按 id 引用，不写 `@ManyToOne`**；真需库守的外键才写 `REFERENCES`。

**删账户不删数据**。原「会话」概念**整个不存在了**，归属改成 `account_id`，这一列 **`NOT NULL` 但不加
外键**：删账户只删账户行，其余五张表的 `account_id` **原样保留**。三种级联都不合适 ——`SET NULL` 抹归属、
`CASCADE` 连带删、`RESTRICT` 让账户永远删不掉。规则落在 Java 侧：工厂方法拿不到账户 id 直接抛
`IllegalArgumentException`，`#requireActive/requireTradable` 再拦一笔，**没有例外**。

## 记录与回放（2026-10-29）

**一个日志 + 一堆快照 + 一段区间投影**，没有第二份真相。

- **写入点只有一个**：`audit_event`。挂载位置应当在「所有状态变化的唯一出口」上（Go 版挂在
  `hub.broadcastInstanceLocked`），这样「机器人操作也被记」不是额外功能，只是挂对地方的自然结果。
- **一步 = 一次操作**，不是「一步 = 一根 K 线」。同一根上画三个图形就是三步 —— 否则回放到那一步
  会一次性把整根的变化全摆出来，看不出用户真实的操作顺序（Go 版 `format 1→2` 就是这个教训，
  旧记录因此不兼容、不混读）。
- **`BASE` 是第 0 步**：开仓**之前**那一屏。它在 `audit_event` 里是普通一行（`action=BASE`），
  `TradeRecord.startSeq` 指着它。它**不带盘面快照** —— 起点盘面由快照表按 `bar_time` 及以上过滤现算，
  不复制一份进 payload。
- **`TradeRecord` 是区间投影**：`[start_seq, end_seq]` 之间的事件就是这笔交易的全过程；
  摘要（盈亏/MFE/MAE/标签）是**独立列**，所以删账户、将来清理事件行，记录照样能列出、能看盈亏。
  代价：**不能按时间窗口批量删 `audit_event`**，否则记录会指向空洞。
- **进度不单独存**：最后一条事件的 `bar_time` 就是（账户, 品种）的进度。
- **全库没有「K 线下标」这种字段**（2026-10-29 清干净）：`audit_event.cursor`、
  `trade_order.placed_index` / `filled_index`、`prop_firm_account.start_bar_index` 全部删掉。
  理由是同一条：**下标是位置，时间是标识**。文件重抓 / 补一根 / 删一根坏行，同一个下标就指向
  另一根 K 线而且不报错；时间永远指向原来那颗。而且下标只在某个标的下成立，账户不绑标的。
  保留的是**序号**（`trade.seq` / `audit_event.seq` / `prop_firm_stage_log.seq` / `audio_clip.seq`，
  用来定同一根 K 线内多件事的先后，推不出来）和 **`Anchor.offset`**（「向右延伸 40 根」是用户意图，
  不是某个下标的值）。
- **已知边界**：图形/语音不做版本化（`Drawing.moveTo` 覆盖端点、删除即删行），所以「全局查看」
  还原的是**现在**的样子，不是平仓那一刻的样子。要真还原得给图形做版本表，或靠
  `ANNOTATION_MOVED/STYLED/DELETED` 重放 —— 后者取决于 payload 存的是完整对象还是增量。
- **不做撤销**（2026-10-29 明确）：append-only 的日志里删事件就不叫「记录在案」了；
  交易类动作（平仓）不可逆，撤销等于改账。盘面类若将来要撤，只能追加补偿事件。

## 起点：开账户时随机抽一根 K 线

`PropFirmStartPicker`（service）：把**所有标的的全部日文件**摊平，随机挑一份，定位到
**当天 09:00 及之后的第一根**可交易 K 线。开账户时由 `PropFirmAccountService#open` 调用，
结果写进账户的 `start_bar_time` / `start_day_file`。

- **抽的是「日文件」不是「日期」**：账户不绑标的，光有日期定位不到具体文件；抽文件自带品种。
  均匀性以文件为单位（某标的抓得多就更容易被抽中），当前 4 个标的起止相同，差异可忽略。
- **为什么顺延而不是报错**：真实数据里有些日子不是从 07:00 开始的。要求「必须正好有一根 09:00」
  会让开账户随日期**随机失败**——一周撞一次，最难查。所以取「09:00 及之后第一根」，
  整天都在 09:00 之前就换下一份文件。
- **抽不到就不建账户**：没有起点的考核，日历首月 / 交易日下界 / 回放起点全部无从算起。
  先抽再落库，抽失败库里零残留。
- **起点只有一个键：`startBarTime`。** 曾经还有个 `startBarIndex`，已删（2026-10-29）——
  下标是**位置**，文件重抓 / 补一根 / 删一根坏行就悄悄指向另一根 K 线（不报错），
  而且只在抽中的那个标的下成立（账户不绑标的，同一个下标在 MES 和 CL 上是两根）。
  名字（时间、文件名）不会因为内容变了而撒谎，位置会。
- **`started_at` ≠ `start_bar_time`**：前者是真实时钟（这行何时写进库），后者是行情时间轴上的位置。
  起点字段都是 `updatable = false`——改一次，这个账户的所有历史都跟着挪位置。

## PF 考核规则

**三阶段挤在同一行账户上**（`PropFirmStage`：EVAL → FUNDED → PAYOUT，单向），历史在 `prop_firm_stage_log`。
升级时余额被强制拨回账户规模，所以**余额不能由成交反推**，每阶段各存 `base_balance`。

- **交易日 = `[D 06:00, 次日 04:45)`**，04:45–06:00 不许交易、不属于任何交易日；不做夏冬令时（CSV 明文当
  北京时间比字符串），`PropFirmClock` 是唯一实现。
- **爆仓逐根判定**（`PropFirmSession`）：浮亏用极值（多单看 low、空单看 high），**等于也算失败**；亏损线只在
  日终上移。每根全量重放 `TradeLedger` 取净头寸。
- **只留一致性 50%**：`目标 = max(模板目标, 单日最大净盈利 / 0.5)`（总盈利 3000 而单日 1800 → 目标抬到
  3600，所以 3000 不能通关）。
- FUNDED 手数按利润分档 0→20 / 1000→30 / 2000→40（`PropFirmPositionLimit`），判每品种净持仓。
- **PAYOUT 只是「不能再下单」**，账户仍 ACTIVE（还能画图录音）→ `Status` 只有 **ACTIVE / FAILED**。

## 标的清单：`_symbols.json` → `symbol` 表（启动时同步）

`SymbolSyncService`（`ApplicationRunner`）每次启动读 `<bar-root>/_symbols.json`，按标的代码 upsert
`folder / tick / tickValue / commission`。**内容没变的行不写**（否则每次启动 `updated_at` 全被刷新，
日志里「更新 4 条」会把真改了费率的那个淹掉）。幂等，改费率重启即生效。
库里有、清单里没有的标的**只 warn 不删**（可能是清单被临时覆盖，删了会让已有成交引用落空）。

**为什么必须有这一步**：`PropFirmStartPicker` 抽账户起点时**只按 `symbol.folder` 找 CSV，从不枚举 `data/`
根目录**。表空着的时候，`data/` 下有几万个 csv 也一个都看不见 —— 这就是 2026-10-30 那次
「明明有数据却报 `data/ 下没有 .csv`」的成因（当时报错文案和真实原因完全对不上，已拆成两条）。

四种情况分开处理：清单不存在（还没抓过行情）= 空转；清单坏了 = 启动日志 ERROR 但不拦启动
（服务还能只读复盘，且开账户时会得到明确提示）；缺 `commission` 的标的跳过（不进库）；
一个 `folder` 撞了 UNIQUE = 跳过该条、不带走整张表。

## 手续费：标的自己的参数，没有价目表

`data/_symbols.json` 每个标的自带 `commission`（**每手每边**美元），Java 侧就是 `symbol.commission` 一列，
**`NOT NULL`**：`0` = 明确免费（`NYMEX:CL1!` 就是这样）、`>0` = 收费，**没有「还没配」这个第三态**。
`Symbol#of(code, folder, commission)` 和 `setCommission` 都收不了 `null`，所以 `perSideCommission` /
`roundTripCommission` 也不会再返回 `null`；数据库列自己也是 NOT NULL（绕过实体直接 insert 会被拦下）。
「往返乘不乘 2」只有 `Symbol#roundTripCommission` 一处回答（= 单边 × 2），散到调用方迟早一处乘一处忘。

Go 侧会**整个重写** `_symbols.json`：0.31.0 起 `commission` 被原样带回去（`*float64`，`0` 不会被
`omitempty` 省掉）。**教训**：`saveXxxLocked` 会重写的文件，用户手写的每个键都必须有对应 struct 字段。
⚠️ Go 侧仍可能写出「没有 `commission` 键」的标的（新标的刚出现时抓取端不知道费率），
这种行**进不了库**（列 NOT NULL），也就是必须在 `_symbols.json` 里补上费率才谈得上建标。

## 全局约定

1. **金额一律 `BigDecimal`、列是 TEXT**：SQLite 没 decimal，`DECIMAL` 会被 NUMERIC 亲和性转成 REAL 丢精度。
2. **时间价格用「明文 / 毫秒整数」**：K 线时间是 CSV 原文，转成时间戳就对不上协议。
3. **能算的不存**（持仓、盈亏）· **快照优于引用**（样式、规则从模板复制）· **主键是 UUID**（`Symbol` 除外）。
4. **成交的先后只认 `trade.seq`**（同一账户内从 1 起单调递增），**不认主键**：id 是 UUID，同一根 K 线上
   多笔成交按它排等于随机排，逐批归因会随读库顺序变。排序统一用 `Trade.executionOrder()`，
   仓储方法一律 `...OrderByBarTimeAscSeqAsc`。`seq` 由写入方在同一事务里取 `max(seq)+1`。
5. **余额只能由 `syncComputed` 追平重算结果**，没有增量记账入口（旧的 `applyNetPnl` 已删）：
   两条算路必然分叉，增量那条容易漏掉开仓那一笔的手续费。
6. **枚举**：一律 `@Enumerated(EnumType.STRING)` 存**常量名**；Go 要小写串，换算只在输出侧做；
   `@Enumerated` 与 `@Convert` 不能同字段；新增动作不能撞小写名。

## 测试 / 改表

`./mvnw test`。覆盖：`PersistenceSmokeTest`（schema/映射/BigDecimal/枚举落库/seq 落库/**事件区间回放**/
`replay_cursor` 表必须不存在）· `PropFirmAccountLifecycleTest`（删账户后子表一行不少、`account_id` 原样保留，
**且那笔交易的事件区间仍然取得到**）· `AccountRequiredTest`（含 `seq` 不能为负、区间不能是反的）·
`TradeLedgerTest` · `TradeLedgerConsistencyTest`（**同一根 K 线上多笔成交必须按 seq 排**，否则逐批归因随读库顺序变）·
`PropFirm*Test` 三支 · `PropFirmStartPickerTest`（09:00 定位 / 顺延 / 跳过不合格日 / 没数据就报错）·
`SymbolCommissionTest` · `CsvBarStoreTest` · 三个 controller 测试。

⚠️ 三个 controller 测试**没覆写 datasource**，跑测试会直接读写 `./db/tradingview-j.db`（既有行为）。
⚠️ `Trade.realizedPnl` 与 `TradeLedger` 现算 realized 是**两处计算**，将来要合并。

`ddl-auto` 是 `none`，表结构全由 `schema.sql` 管（SQLite 的 `ALTER TABLE` 支持极有限，Hibernate 的 `update`
会生成它不支持的 DDL，且只在「第二次启动且字段变了」时失败）。**改表结构必须重建 `./db/`**：`schema.sql`
全是 `CREATE IF NOT EXISTS`，对旧表什么都不做，启动时报 `no such column`。没有迁移脚本，挪走旧库即可：
`mv db/tradingview-j.db db/tradingview-j.db.bak-$(date +%Y%m%d)-<原因>`。反过来**删表 `schema.sql` 永远做不到**，
必须手 `DROP`（2026-10-29 删 `indicator` 就是 `DROP TABLE` + `VACUUM`；同一天删 `replay_cursor`，
旧库里那张表还在，要手 `DROP TABLE IF EXISTS replay_cursor;`）。

**2026-10-29 的 `audit_event` 加列已经按这条规则处理过一次**：旧库没有 `symbol_id`/`bar_time`，
启动会在建索引那一步报 `no such column: symbol_id`。现场是
`db/tradingview-j.db.bak-20261029-audit-event-range`。同一天加 `prop_firm_account.start_*` 与
`trade_record.account_name` 又处理了一次：`db/tradingview-j.db.bak-20261029-account-start-and-name`。

⚠️ 加 NOT NULL 列**不会**让 controller 测试变红（它们不往这两张表插数据），所以「测试全绿」
不等于「旧库能用」。改完表结构要自己 `pragma table_info` 看一眼真实列。
