# tradingview-j-server

Go 版 `tradingview-j` 的 Java / Spring Boot 迁移目标。当前有两条链路：版本号接口（用来确认服务起得来），
以及**领域层 + SQLite 持久化**（13 个业务概念的实体、仓储与交易账本）。复盘/抓取的 HTTP 与 WS 接口还没搬。

工程文件（`pom.xml`、`src/`、`mvnw`）就在**仓库根目录**，和 Go 模块共用同一个根：

- Java 侧走 Maven：`src/`、`target/`、`mvnw`
- Go 侧走 go build：`main.go`、`internal/`、`build.cmd`/`build.sh`
- 两边互不引用，各自的产物都在自己的忽略规则里

## 跑

```bash
./mvnw spring-boot:run      # Windows: mvnw.cmd spring-boot:run
```

访问 http://127.0.0.1:8791/api/version → `{"code":0,"msg":"ok","data":{"version":"1.0.0","application":"tradingview-j-server"}}`

首次启动会自动建 `./db/tradingview-j.db`（SQLite，`schema.sql` 里全是 `IF NOT EXISTS`，重复执行无害）。
`db/` 已加进 `.gitignore`：它是本机运行期状态，和入库的 `data/` 行情数据不是一回事。

打包：`./mvnw package` → `target/tv-server.jar`，`java -jar target/tv-server.jar`。

## 接口文档（Swagger UI）

服务起来后直接开：

- 页面：http://127.0.0.1:8791/swagger-ui.html （会 302 到 `/swagger-ui/index.html`，记短地址就行）
- JSON：http://127.0.0.1:8791/v3/api-docs

用的是 **springdoc-openapi 2.8.16**，不是 springfox——springfox 2020 年就停更了，和 Boot 3 的 Jakarta 命名空间不兼容。版本线必须和 Boot 大版本对齐：`2.8.x` 配 Boot 3.4/3.5，`3.x` 是给 Boot 4 的，混用启动就报类找不到。

接口说明写在代码上（`@Tag` / `@Operation` / `@Schema`），文档跟着接口走，不改两处。整体定义和服务器地址在 `OpenApiConfig`。

### 两个必须知道的坑

**1. `/v3/api-docs` 不能被 `GlobalResponseAdvice` 包住。**

这个 advice 会包所有 Controller 返回值，而 springdoc 的文档端点本身也是个 Controller。一旦被包成 `{"code":0,"msg":"ok","data":{...}}`，Swagger UI 在顶层找不到 `openapi` 字段，页面**白屏**，控制台只报一句「找不到 spec」，完全联想不到是统一响应体干的。所以 `GlobalResponseAdvice` 里 `UNWRAPPED_PACKAGES` 有 `org.springdoc` 这一条。

**2. 文档里的响应结构要和真实返回一致。**

Controller 返回的是裸 `VersionResponse`，`code/msg/data` 是运行期补的。springdoc 只看得见方法签名，会写成「响应 = VersionResponse」，前端照着写 `res.version` 就错了（正确是 `res.data.version`）。`OpenApiConfig#envelopeAwareResponseCustomizer` 在生成文档后把每个响应体套上外壳，原来的模型挂到 `data` 上。

`OpenApiDocsTest` 同时钉住这两条：顶层没有 `code` 字段、`data` 的 `$ref` 指向 `VersionResponse`。谁把 `org.springdoc` 从排除名单删了，测试当场红。

### 配置

`application.yml` 的 `springdoc` 段：`paths-to-match: /api/**` 只收录业务接口；开了 try-it-out（服务只绑 127.0.0.1，外部发不到请求）。**不要同时配 `packages-to-scan`**——两者是「与」关系，包名写错会得到一份空文档。

## 版本号只有一处

`pom.xml` 的 `<version>` → Maven 资源过滤写入 `application.yml` 的 `app.version` → `AppProperties` → Controller。
升版本只改 pom。测试 `VersionControllerTest` 钉住 1.0.0，过滤失效会当场红。


## 分层与数据落在哪

```
controller  →  service  →  repository  →  SQLite（./db/tradingview-j.db）
                    ↘  storage  →  data/<folder>/*.csv（K 线，唯一不进库的数据）
```

| 层 | 包 | 只做一件事 |
| --- | --- | --- |
| 接口 | `controller` | HTTP 入参出参，不碰实体 |
| 出入口对象 | `dto` | 网线上的形状（当前只有 `VersionResponse`） |
| 领域 | `entity` | JPA 实体 + 值对象 + 枚举，按聚合分子包 |
| 写入 | `repository` | 每个聚合根一个 Spring Data 接口，不写业务判断 |
| 编排 | `service` | 事务边界 + 领域计算（`TradeLedger` 是唯一算盈亏的地方） |
| 文件 | `storage` | K 线 CSV / 音频 WAV，不碰数据库 |

**为什么 K 线不进库**：一天一个文件、一个标的几百个文件；CSV 人类可读、能 diff、能拿编辑器打开；
抓取侧（Go 程序）已经在往里写，两边共用一种格式比两边各写一份库稳。代价是「按时间范围跨日查询」
要 readdir + 逐文件读，但那正是现在的用法。

**为什么业务事实进库**：图形、挂单、成交、语音、交易记录、PF 账户这些东西的**一致性要求**远高于
K 线，而且需要按条件查（这个账户的记录、这条记录的步骤、这个账户还挂着的单）。CSV 做不到这些。

## 实体设计（13 个业务概念，PF 账户是唯一聚合根）

| 概念 | 实体 | 包 | 关键设计 |
| --- | --- | --- | --- |
| 标的 | `Symbol` | `entity.market` | 主键就是标的代码；`folder` 是 CSV 目录的锚；tick/tickValue/commission 只存在这一处 |
| PF 模板 | `PropFirmTemplate` | `entity.propfirm` | 只是价目表；不做版本化 |
| PF 账户 | `PropFirmAccount` | `entity.propfirm` | **一切操作的唯一归属点**；规则是开账户那一刻的副本；删它不删任何子数据 |
| 指标 | `Indicator` | `entity.indicator` | 只存**定义**（类型+周期+来源），值靠 OHLC 现算 |
| 绘图图形 | `Drawing` | `entity.drawing` | 五种图形共用「起点+终点」；样式是**画下去那一刻的快照**；只有用户手动删除才消失 |
| 下单 | `Order` | `entity.trade` | 没有「止损单/止盈单」类型；`ocoGroup` 保证同根不双触发 |
| 交易 | `Trade` | `entity.trade` | **整个交易域的唯一真相**；持仓和盈亏由它重放得出 |
| （派生） | `Position` / `ClosedLot` | `entity.trade` | **不落库**。均价法给账户看，FIFO 逐批给画线看 |
| 录音 | `AudioClip` | `entity.voice` | 只存元数据 + 相对路径；同一根 K 线最多 9 段 |
| 操作记录 | `AuditEvent` | `entity.audit` | append-only；`payloadJson` 存原始负载，不用 `Object` |
| 策略 | `Strategy` | `entity.strategy` | 只存注册信息，**算法必须是 Java 类**，不能从库里加载 |
| 策略执行机器人 | `StrategyBot` | `entity.strategy` | 一次运行一行；只存摘要，运行期逐拍状态在内存 |
| 交易记录 | `TradeRecord` | `entity.record` | 一笔完整交易：起点 + 步骤 + 结算摘要；生命周期自动 |
| （步骤） | `TradeRecordStep` | `entity.record` | 一步 = 一次真实操作，第 0 步是「开仓前那一屏」 |

### 手续费：标的自己的一个参数，没有价目表

`data/_symbols.json` 里每个标的自己带一个 `commission`（**每手每边**多少美元，和 `tick` / `tickValue` 并列）：

```json
"CME_MINI:MES1!": { "folder": "CME_MINI_MES1!", "tick": 0.25, "tickValue": 1.25, "commission": 0.50 }
```

Java 侧就是 `symbol.commission` 一列，跟着标的一起读写。计费口径是**内建**的：按手数、**单边收费** ——
一笔完整的交易开仓收一次、平仓再收一次，所以 `Symbol#roundTripCommission(手数)` = 单边 × 2，
`Symbol#perSideCommission(手数)` 只算一次。「往返乘不乘 2」只有这一个地方回答，
散到调用方迟早一处乘了、一处忘了，而这种错不抛异常，只是安静地少算一半成本。

`null` 和 `0` 必须分开：`null` = 这只标的还没配，`0` = 明确写了 `"commission": 0`，也就是免手续费
（`NYMEX:CL1!` 就是这样）。方法在没配时返回 `null` 而不是 0，否则漏配会按免费结算。

**JSON 和库的关系**：Go 抓取侧不算账，但它会按自己的结构**整个重写** `_symbols.json`
（`internal/store/store.go: saveIndexLocked`）。从 0.31.0 起它把这个 `commission` 原样带回去
（`symRec.Commission` 是 `*float64`，所以 `0` 不会被 `omitempty` 连同「没配」一起省掉），
在此之前它会在分配新标的文件夹的那一刻被静默抹掉。

### 关系（全部按 id 引用，不写 `@ManyToOne`）

```
Symbol ──1:N──> PropFirmAccount ──1:N──> Drawing / Order / Trade / AudioClip / AuditEvent
                              │            / Indicator / StrategyBot / TradeRecord
                              └─1:N──> TradeRecord ──1:N──> TradeRecordStep
                                                     └── Trade（反向引用 trade_record_id）
PropFirmTemplate ──1:N（规则副本）──> PropFirmAccount
Strategy ──1:N──> StrategyBot
```

真正需要「数据库守着」的那几处外键（图形 → 斐波那契线、记录 → 步骤/标签、账户 → 标的）
写在 `schema.sql` 的 `REFERENCES` 上，由 SQLite 自己守（连接串里 `PRAGMA foreign_keys = ON`）。
`account_id` 这一列是例外，故意不加外键，理由见下一节。
不在实体上写 `@ManyToOne` 的理由：聚合之间的引用本来就只是「知道对方 id」，
用 ORM 关联换来的是一次懒加载异常和一堆 fetch join。

### 账户只是统计点：删账户不删数据

原来是「会话」把图形、挂单、成交、语音、操作记录、指标、机器人、交易记录收在一起（`ON DELETE CASCADE`）。
现在**会话这个概念整个不存在了**，归属改成 `accountId`，这一列 **`NOT NULL` 但不加外键**：

| 被删的东西 | 会发生什么 |
| --- | --- |
| PF 账户（`prop_firm_account` 一行） | 只删这一行。另外七张表（指标 / 图形 / 语音 / 交易记录 / 挂单 / 成交 / 机器人）的 `account_id` **原样保留**（指向一个已经不存在的账户），**一行子数据都不删、一个字节都不改** |
| 图形（`drawing`） | 只能由用户自己删；删图形会带走它的斐波那契水平线（真级联，那是图形的一部分） |
| 交易记录（`trade_record`） | 只能由用户自己删；删记录会带走它的步骤和标签（真级联） |
| 操作记录（`audit_event`） | 不加外键，账户删了仍保留原 `account_id`。任何改写都等于伪造审计 |
| 挂单 / 成交 / 语音 / 指标 / 机器人 | 保留，`account_id` 原样留着 |

两条连锁规矩：

1. **没有账户就不能有操作。** 各实体的工厂方法（`Drawing.of` / `Order.place` / `Trade.fill` /
   `AudioClip.record` / `Indicator.of` / `StrategyBot.start` / `TradeRecord.open`）拿不到账户 id 直接抛
   `IllegalArgumentException`；`PropFirmAccountService#requireActive` 再拦「账户不存在 / 已经不是 ACTIVE」。
   只有 `AuditEvent.of` 允许空账户 —— 流水账里有全局操作。
2. **`account_id` 是 `NOT NULL` 且不加外键。** 三种级联策略都不合适：`SET NULL` 会把归属
   抹掉、`CASCADE` 会连带删数据、`RESTRICT` 会让账户一旦有数据就永远删不掉。
   所以「必须属于某个账户」这条规则落在 Java 侧（各实体工厂方法 +
   `PropFirmAccountService#requireActive`），数据库不对这一列设引用约束。
   换来的是「删账户绝不带走用户数据、也不篡改历史上归属」这条硬保证。

### 五条贯穿全局的约定

1. **金额一律 `BigDecimal`，对应列是 TEXT。** SQLite 没有 decimal，`DECIMAL` 会被 NUMERIC 亲和性
   转成 REAL 丢精度。由 `BigDecimalTextConverter` 在边界上用字符串往返。
2. **时间和价格都用「明文 / 毫秒整数」。** K 线时间是 CSV 里那一列的原文（`"2026-10-16 12:00"`），
   转成时间戳就再也对不上协议；其它时间戳走 `InstantEpochConverter` 存 Unix 毫秒。
3. **能算的不存。** 持仓、已实现盈亏、指标值、斐波那契四条线的价格，全部现算。
   存副本迟早和事实对不上，而对不上时没有任何办法判断谁对。
4. **快照优于引用。** 图形样式从模板复制、账户规则从模板复制。
   模板是「我常用的那套」，改了不该回头改动已经存在的东西。
5. **主键是 UUID 字符串。** 对象先有 id 再落库（图形画下去那一刻 id 就广播出去了），
   合并/导入不撞号。`Symbol` 除外，它的主键就是标的代码。

## 枚举清单

**凡是取值域封闭的东西一律是枚举，不写成 `String`。** 当前 13 个：

| 枚举 | 位置 | 取值 | 落库值 |
| --- | --- | --- | --- |
| `Actor` | `enums/`（4 处共用） | HUMAN / BOT / SYSTEM | 常量名 `HUMAN` `BOT` `SYSTEM` |
| `ReplayAction` | `enums/`（2 处共用） | 19 个动作 + BASE | 常量名 `BAR_ADVANCED` … |
| `Side` | `entity.trade` | BUY / SELL | `BUY` `SELL` |
| `Order.Type` | 嵌套 | LIMIT / STOP / MARKET | 常量名 |
| `Order.Status` | 嵌套 | OPEN / FILLED / CANCELLED | 常量名 |
| `Drawing.Kind` | 嵌套 | TREND / RAY / RECT / ARROW / FIB | 常量名 |
| `DrawingStyle.Dash` | 嵌套 | SOLID / DASHED | 常量名 |
| `Indicator.Type` | 嵌套 | EMA / SMA / RMA / WMA / ATR | 常量名 |
| `Indicator.Source` | 嵌套 | OPEN / HIGH / LOW / CLOSE / HL2 | 常量名 |
| `StrategyBot.Status` | 嵌套 | RUNNING / STOPPED / FINISHED / FAILED | 常量名 |
| `StrategyBot.StopReason` | 嵌套 | MANUAL / DATA_END / ACCOUNT_CLOSED / ERROR | 常量名 |
| `PropFirmRules.DrawdownType` | 嵌套 | STATIC / TRAILING_INTRADAY / TRAILING_EOD | 常量名 |
| `PropFirmAccount.Status` | 嵌套 | ACTIVE / PASSED / FAILED | 常量名 |

三条约定：

1. **一切枚举都用 `@Enumerated(EnumType.STRING)` 存常量名，没有转换器。**
   Go 协议里 `by`、`action`、`step.kind` 用的是小写串（`"human"` / `"bar_advanced"`），
   换算规则只有一条：**常量名转小写**（`name().toLowerCase()`）。这个换算在输出侧
   （DTO 或前端）做，持久化层不为客户端格式让步 —— 库里存的是 `HUMAN` / `BAR_ADVANCED`。
   `PersistenceSmokeTest` 直接查 `select action from audit_event` 断言拿到 `BAR_ADVANCED`，
   谁把它换回小写线值的 `AttributeConverter`，测试当场红。
   推论：**新增动作时不能有两个常量小写后撞名**，否则协议侧分不开。
2. **`@Enumerated` 和 `@Convert` 不能同时用在同一个字段上**，不要在枚举字段上加转换器。
3. **`TradeRecord` 没有自己的来源枚举**：它的「这些操作是谁做的」就是 `Actor`，
   Go 里那两个看起来不同的概念（`CreatedBy` = 谁开的盘面 / `Source` = 谁动的）在
   业务上问的是后一个问题。`SYSTEM` 不是一笔交易的来源，由 `TradeRecord.open()` 拦掉。

自由文本的取值域一个都没留：`stopReason` 曾经是 `String`，
第一次做筛选就会写成「数据到头 / 跑到末尾 / EOF」三种口径，所以它是枚举。

## 测试

| 测试 | 钉住什么 |
| --- | --- |
| `PersistenceSmokeTest` | 每类实体存一次读一次：schema 与映射一致、BigDecimal 一位不差、Instant 往返、派生方法名查询能用 |
| `PropFirmAccountLifecycleTest` | 两条账户规则的验收：没有账户/账户已删就不能动手；删账户后八类子数据一行不少，只有归属列变成 NULL（`audit_event` 连归属都保留） |
| `AccountRequiredTest` | 各实体工厂方法在没给账户时当场抛异常（纯单元测试，不起 Spring） |
| `TradeLedgerTest` | 均价法与 FIFO 逐批的关系：全平时必须相等、部分平时必须不等、反手要重置均价 |
| `CsvBarStoreTest` | 跳过表头、跳过坏行不炸、`folder` 不能带 `..` |
| `VersionControllerTest` / `OpenApiDocsTest` / `ResponseEnvelopeTest` | 统一响应体与 Swagger 文档 |

跑：`./mvnw test`（SQLite 用临时库；`VersionControllerTest` / `OpenApiDocsTest` / `ResponseEnvelopeTest`
仍用 `./db/` 那份真库，所以**改了表结构要先重建 `./db/`**，见文末）。

## 文件

| 文件 | 作用 |
| --- | --- |
| `src/main/java/.../ServerApplication.java` | 入口，对应 Go 的 `main.go` |
| `.../controller/VersionController.java` | `GET /api/version` |
| `.../dto/` | 接口出入口对象（原来的 `entity/VersionResponse` 搬到这里） |
| `.../entity/**` | 9 个聚合子包：market / propfirm / indicator / drawing / trade / voice / audit / strategy / record |
| `.../repository/**` | 13 个 Spring Data JPA 接口 |
| `.../service/PropFirmAccountService.java` | 开账户 / 删账户（只删一行，不级联）/ `requireActive` 开工闸门 |
| `.../service/TradeLedger.java` | 成交流水 → 持仓 / 已实现盈亏 / 逐批归因，全书唯一一处这样算 |
| `.../storage/CsvBarStore.java` | 读 `data/<folder>/<日期>.csv`（只读，不写） |
| `.../storage/Bar.java` | K 线值对象，不入库 |
| `.../config/AppProperties.java` | 绑定 `app.*`（版本号、`barRoot`） |
| `.../config/OpenApiConfig.java` | Swagger 文档定义 + 把 `code/msg/data` 还原进文档 |
| `.../web/GlobalResponseAdvice.java` | 统一响应包裹（排除 `org.springdoc`，否则文档白屏） |
| `src/main/resources/schema.sql` | 全部 DDL，`CREATE ... IF NOT EXISTS`，每次启动执行 |
| `src/main/resources/application.yml` | 端口 8791、只绑 127.0.0.1、SQLite 连接（池大小锁死 1） |
| `db/` | SQLite 库文件（不入库，只留 `.gitkeep`） |

**`spring.jpa.hibernate.ddl-auto` 是 `none`。** 表结构由 `schema.sql` 管：
SQLite 的 `ALTER TABLE` 只支持很有限的形式，Hibernate 的 `update` 会生成它不支持的 DDL，
而且这种失败只在「第二次启动且字段变了」时才出现 —— 属于最难查的一类问题。

### 改表结构之后：`./db/` 要重建，没有迁移脚本

`schema.sql` 全是 `CREATE ... IF NOT EXISTS`，对**已存在的旧表**它什么都不做，
于是新加的列不存在、新加的外键也不存在，启动时报的是
`Failed to execute SQL script statement #N ... CREATE INDEX ... no such column`。
本项目没有写迁移脚本（单用户本机工具，`db/` 是运行期状态，不是资产），
所以**改结构之后要先把旧库挪走**再启动：

```bash
mv db/tradingview-j.db db/tradingview-j.db.bak-$(date +%Y%m%d)
```

2026-10-29 这次「删掉会话、改用 PF 账户」就是这么处理的，旧库被改名为
`db/tradingview-j.db.bak-20261029-pre-account-model`（当时各表都是 0 行，没有数据损失）。
