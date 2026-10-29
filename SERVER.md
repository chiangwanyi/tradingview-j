# tradingview-j-server

Go 版 `tradingview-j` 的 Java / Spring Boot 迁移目标。当前只有一条链路：起得来、打日志、读配置、返回版本号。

工程文件（`pom.xml`、`src/`、`mvnw`）就在**仓库根目录**，和 Go 模块共用同一个根：

- Java 侧走 Maven：`src/`、`target/`、`mvnw`
- Go 侧走 go build：`main.go`、`internal/`、`build.cmd`/`build.sh`
- 两边互不引用，各自的产物都在自己的忽略规则里

## 跑

```bash
./mvnw spring-boot:run      # Windows: mvnw.cmd spring-boot:run
```

访问 http://127.0.0.1:8791/api/version → `{"version":"1.0.0","application":"tradingview-j-server"}`

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

## 实体层（Go 数据结构的 Java 镜像）

Java 侧**不负责抓 K 线**（那是 Go 的活：CDP 接入、WS 帧解码、CSV 落盘全留在 `internal/`）。
所以实体层只镜像「读已有数据 + 复盘 + 报告」用得上的那部分，按 Go 的包分子包，
类名、字段名、JSON 键全部照抄，**只看类名就能回去搜 Go**。

Go 那边 `internal/replay` 是一个包塞了七十来个类型，Java 平铺一个包会翻不动，所以在 Go 包下面
**再按业务概念分一层**：包名比 Go 多一段，类名不变。分包只影响 `import`，不影响 JSON——
线上形状由 `EntityJsonTest` 钉住，搬包不需要动前端。

| Java 包 | 对应 Go | 内容 |
| --- | --- | --- |
| `entity.store.scan` | `internal/store` | 扫描算出来的：`ScanResult` / `FolderStat` / `DayStat` / `RowStat` / `Gap` / `DateTotal` / `BarCount` / `WeekExpect` |
| `entity.store.catalog` | `internal/store` | 磁盘格式：`SymbolIndex` / `SymRec`（`_symbols.json`）、`CatalogEntry`（选标的名录）、`TimeBar`（读回的 CSV 行） |
| `entity.report.summary` | `internal/webreport` | 整份快照的骨架：`Report` / `Meta` / `Totals` / `Span` |
| `entity.report.detail` | `internal/webreport` | 明细层：`Folder` 与它下面的 `Day` |
| `entity.replay.market` | `internal/replay` | 行情：`Bar` / `Point` / `Timeframe` / `SymbolInfo`，以及推进 K 线的两个负载 |
| `entity.replay.trade` | `internal/replay` | 订单 / 成交 / 持仓 / 盈亏统计，与下单、改单、全平的负载 |
| `entity.replay.annotation` | `internal/replay` | 标注、样式快照、斐波那契、全局模板 |
| `entity.replay.voice` | `internal/replay` | `AudioClip` / `VoiceState` / 语音负载 / `StepContext` |
| `entity.replay.recording` | `internal/replay` | 录像本体、步骤、事件、摘要、索引、打标 |
| `entity.replay.instance` | `internal/replay` | 实例快照、元信息、存档格式（含 `Legacy*`）、操作日志 `Event` |
| `entity.replay.protocol` | `internal/replay` | 连接与上行：`Request`、`Welcome` / `Pong` / `Left` / `Error` / `Deleted`、成员与控制权 |
| `entity.replay.strategy` | `internal/replay` | 「有哪些策略可以跑」的协议形态 |
| `entity.strategy.input` | `internal/strategy` | 策略输入：`StrategyBar` / `StrategyPosition` / `StrategyContext` |
| `entity.strategy.output` | `internal/strategy` | 策略输出：`Signal` 与描述 `StrategyInfo` / `StrategyParam` |

**没有搬的（抓取侧，别往回加）**：`internal/config`（config.json 全是抓取/端口配置，Java 用 `application.yml`）、
`internal/cdp`、`internal/tvproto`、`internal/ingest`、`internal/capture`，以及 `store` 里只服务抓取的
`Bar`（落盘用的最小 K 线，只有 `ingest` 在造）和 `SymbolStat`（会话内累计写入统计）。

**只搬了数据，没搬行为。** 带锁、带 channel、带未导出字段的运行期对象（`store.Store`、`Scanner`、
`replay.Hub`、`replay.Instance`、`recording.tradeRecorder` …）留在将来当 Service 写，它们不是实体。
同理 `*_test.go` 里的替身一个没搬。

三个必须知道的约定：

1. **重名类靠子包区分**。Go 里 `store.TimeBar` 与 `replay.Bar` 是两个不同的东西
   （读文件的中间态 / 送前端的 K 线），Java 里就是两个包下的两个类，不要合并。
   同理 `strategy.Info` 与 `replay.StrategyInfo` 也是两个类型：`entity.strategy.output.StrategyInfo`
   是策略层描述，`entity.replay.strategy.StrategyInfo` 是协议形态（多 `desc` 字段和 Jackson 注解）。
   目前两份都在，**合并属于业务模型变更，得先确认**。
2. **类上都有 `@JsonInclude(ALWAYS)`**。`application.yml` 全局是 `non_null`，但 Go 的协议里
   数组字段没写 omitempty 时是 **`null` 而不是消失**——前端拿 `msg.orders.length` 会当场抛错。
   所以实体类一律 ALWAYS，Go 里带 `omitempty` 的字段在**字段上**单独标 `NON_DEFAULT`（字段级优先于类级）。
3. **`Recording extends RecordingSummary`** 是 Go 的内嵌结构体，JSON 是平的（摘要字段和记录字段同一个对象）。
   两边都用 `@SuperBuilder`，别改成 `@Builder`——那会丢掉父类字段。

## 文件

| 文件 | 作用 |
| --- | --- |
| `src/main/java/.../ServerApplication.java` | 入口，对应 Go 的 `main.go` |
| `.../controller/VersionController.java` | `GET /api/version` |
| `.../entity/VersionResponse.java` | 响应体（Lombok `@Data`/`@Builder`） |
| `.../entity/**` | 95 个数据类分布在 14 个子包，Go 侧「读数据 / 复盘 / 报告」那部分结构的镜像，见上面「实体层」一节（抓取侧的 cdp / tvproto / ingest / capture / config 一概没有） |
| `src/test/java/.../entity/EntityJsonTest.java` | 钉住 `ema20:null`、`offset` 省略、`filledIndex:-1`、录像字段扁平 |
| `.../config/AppProperties.java` | 绑定 `app.*`，对应 Go 的 `config.json` |
| `.../config/OpenApiConfig.java` | Swagger 文档定义 + 把 `code/msg/data` 还原进文档 |
| `.../web/GlobalResponseAdvice.java` | 统一响应包裹（排除 `org.springdoc`，否则文档白屏） |
| `src/main/resources/application.yml` | 端口 8791、只绑 127.0.0.1（Go 的复盘服务占 8790） |
| `src/main/resources/logback-spring.xml` | 控制台 + `logs/` 按天滚动，留 30 天 |

没有数据库、没有 JPA/MyBatis —— 数据仍是 Go 版写出的 CSV。
