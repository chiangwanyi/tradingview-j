# tradingview-j-server

Go 版 `tradingview-j` 的 Java / Spring Boot 迁移目标，工程文件（`pom.xml` / `src/` / `mvnw`）在**仓库根**，
与 Go 模块共用一个根、**互不引用**。已有版本号接口 + 领域层与 SQLite 持久化；复盘/抓取的接口还没搬。

## 跑

`./mvnw spring-boot:run`（Windows 用 `mvnw.cmd`）。端口 **8791**、只绑本机。
`GET /api/version` → `{"code":0,"msg":"ok","data":{...}}`；Swagger 在 `/swagger-ui.html`，用
**springdoc-openapi 2.8.x**（配 Boot 3.4/3.5；`3.x` 给 Boot 4）。首次启动自动建 `./db/tradingview-j.db`。

**Swagger 两个坑**（`OpenApiDocsTest` 钉住）：`UNWRAPPED_PACKAGES` 必须含 `org.springdoc`，否则文档端点也被
包成 `{code,msg,data}`、页面**白屏**；响应结构也要补外壳（springdoc 只看方法签名），由 `OpenApiConfig` 做。

版本号只有一处：`pom.xml` 的 `<version>` → 资源过滤 → `app.version` → `AppProperties`。

## 分层

`controller → service → repository → SQLite(./db/)`，旁支 `service → storage → data/<folder>/*.csv`。
`dto` 网线形状 · `entity` 按聚合分子包 · `repository` 每聚合根一个接口、不写业务 · `service` 是事务边界与领域
计算（`TradeLedger` 是**唯一**算盈亏的地方）。K 线不进库：一天一个文件、能 diff、Go 侧已在写。

## 数据模型（12 个业务概念，PF 账户是记账数据的唯一聚合根）

| 概念 | 实体（包） | 关键设计 |
|---|---|---|
| 标的 | `Symbol`（market） | 主键就是代码；`folder` 是 CSV 目录锚；tick/tickValue/commission 只存这 |
| PF 模板 / 账户 | `PropFirmTemplate` / `PropFirmAccount`（propfirm） | 模板 `stage`+`nextTemplateId` 串成三阶段链；账户是**唯一归属点**，带 `stage`/`baseBalance`/`eodPeakBalance`，规则是建户那刻的副本 |
| 绘图 / 下单 | `Drawing` / `Order` | 样式是**画下那刻的快照**；`Drawing` **不带 `account_id`**（挂在 K 线上）；没有止损/止盈单类型，`ocoGroup` 管同根不双触发 |
| 交易 | `Trade`（trade） | **交易域的唯一真相**，持仓与盈亏由它重放；`commission` 是成交那刻的快照；`Position`/`ClosedLot` **不落库**（均价法给账户，FIFO 给画线） |
| 录音 / 审计 | `AudioClip` / `AuditEvent` | 每根最多 9 段；`AudioClip` **不带 `account_id`**（挂在 K 线上）；审计 append-only |
| 策略 / 机器人 | `Strategy` / `StrategyBot` | 算法必须是 Java 类 |
| 交易记录 | `TradeRecord` / `TradeRecordStep` | 一笔 = 起点 + 步骤 + 结算摘要，第 0 步是「开仓前那一屏」 |

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
4. **枚举**：一律 `@Enumerated(EnumType.STRING)` 存**常量名**；Go 要小写串，换算只在输出侧做；
   `@Enumerated` 与 `@Convert` 不能同字段；新增动作不能撞小写名。

## 测试 / 改表

`./mvnw test`。覆盖：`PersistenceSmokeTest`（schema/映射/BigDecimal/枚举落库）· `PropFirmAccountLifecycleTest`
（删账户后子表一行不少且 `account_id` 原样保留）· `AccountRequiredTest` · `TradeLedgerTest` · `PropFirm*Test`
三支 · `SymbolCommissionTest` · `CsvBarStoreTest` · 三个 controller 测试。

⚠️ 三个 controller 测试**没覆写 datasource**，跑测试会直接读写 `./db/tradingview-j.db`（既有行为）。
⚠️ `Trade.realizedPnl` 与 `TradeLedger` 现算 realized 是**两处计算**，将来要合并。

`ddl-auto` 是 `none`，表结构全由 `schema.sql` 管（SQLite 的 `ALTER TABLE` 支持极有限，Hibernate 的 `update`
会生成它不支持的 DDL，且只在「第二次启动且字段变了」时失败）。**改表结构必须重建 `./db/`**：`schema.sql`
全是 `CREATE IF NOT EXISTS`，对旧表什么都不做，启动时报 `no such column`。没有迁移脚本，挪走旧库即可：
`mv db/tradingview-j.db db/tradingview-j.db.bak-$(date +%Y%m%d)-<原因>`。反过来**删表 `schema.sql` 永远做不到**，
必须手 `DROP`（2026-10-29 删 `indicator` 就是 `DROP TABLE` + `VACUUM`）。
