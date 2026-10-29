# 架构决策记录（ADR）

简化版 ADR。状态只有三种：**已生效**（代码就是这么做的）、**已废弃**（做过但撤了）、
**待决策**（还没定，不得当成约束）。

已生效的决策来自代码与文件头注释，是**对现状的解释**，不构成未来架构的强制约束；
要改它们需要新开一条决策并说明迁移方式。

---

## ADR-001 只做被动监听，不注入脚本、不主动拖图

- 状态：已生效
- 背景：主动交互会让浏览器行为不像人工操作，且需要维护注入脚本。
- 决策：只读页面已经收到的 WS 帧；不刷新、不注入、不批量调度；由此连带「不接受命令行参数」。
- 后果：抓取范围受用户手动操作限制；换来看不到的风控暴露面和零 JS 维护成本。
- 证据：`internal/capture/capture.go`、`README.md`「为什么只做被动监听」。

## ADR-002 Go 侧零第三方依赖，产物是单 exe

- 状态：已生效
- 背景：唯一需要的外部能力是 WebSocket 客户端，标准库可完全实现。
- 决策：`go.mod` 不引入 `require`；自写 `wsclient` 处理 64 位长度、分片、掩码。
- 后果：部署简单；代价是协议层细节要自己维护并有测试兜底。
- 证据：`go.mod`、`internal/wsclient/ws.go`。

## ADR-003 `internal/` 扁平，`main` 留在根目录

- 状态：已生效
- 背景：只有一个可执行文件，使用方式是双击；Go 的导入路径即包路径。
- 决策：不建 `cmd/`、`pkg/`；`main.go` 只保留 `main()` 与 `version`。
- 后果：包数即职责数，导入路径短；且 `.syso` 只链同目录包，搬走 `main` 会让 exe 属性页静默变空。
- 证据：`main.go`、`version_test.go`。

## ADR-004 CSV 明文 + 整体重写落盘

- 状态：已生效
- 背景：需要人能直接用 Excel/编辑器核对；同一时间戳会被反复推送。
- 决策：内存按 `(标的, 日期)` 累积，脏文件 tmp+rename 整体重写；时间列是本地时区明文。
- 后果：重复数据在结构上不可能出现；代价是文件不能追加写、按天全量重写。
- 证据：`internal/store/store.go`。

## ADR-005 统计只有一个实现、报告只有一个出口

- 状态：已生效
- 背景：终端表格与网页各算一遍时，同一数据目录会给出两个数字，用户无法裁决。
- 决策：数据质量统计全部在 `store.Scan`；报告只由菜单 1 的分析页输出；终端报表已删除。
- 后果：前端只负责排版；`webreport/testdata/format.test.cjs` 成为格式化期望值的唯一来源。
- 证据：`internal/store/scan.go`、`internal/webreport/`。

## ADR-006 「实时」靠重扫 + 磁盘指纹，不靠推送

- 状态：已生效
- 背景：屏幕上任何数字都应能在 CSV 里核对。
- 决策：抓取只写盘；页面每 2 秒轮询；服务端比对「文件名 + 大小 + 修改时刻」决定是否重扫。
- 后果：延迟 1~2 秒；禁止加事件总线或让页面直接读抓取进程内存。
- 证据：`internal/webreport/server.go`、`store.Scanner`。

## ADR-007 盘面状态的唯一权威在后端，前端只预览

- 状态：已生效
- 背景：多人同时观看同一实例，本地改状态会产生分叉。
- 决策：除缩放/平移/十字光标外，所有操作发给后端，由后端校验、编号、广播；前端以广播为准。
- 例外：nATR 定手数在前端算（只算手数、不算钱）；Ctrl 吸附两份实现但必须同口径。
- 证据：`replay/hub.go`、`web/src/stores/instance-reducer.ts`、`web/src/lib/natr.ts`。

## ADR-008 交易记录挂在唯一广播出口，而不是散在各操作里

- 状态：已生效
- 背景：手工在每处 `do*Locked` 补一行，新增动作时必漏，表现为回放少一步。
- 决策：录制入口挂在广播函数上，录的是广播原文；新增动作天然被录，机器人操作同样被录。
- 后果：记录与实时画面必然一致；代价是广播层承担了录制职责。
- 证据：`replay/hub.go: broadcastInstanceLocked`、`replay/trade.go`。

## ADR-009 回放与实时共用同一个 reducer

- 状态：已生效
- 背景：另写一套播放逻辑会与实时行为漂移。
- 决策：回放 = 深拷起点快照 + 依次喂事件；同一份 `applyInstanceEvent`。
- 后果：回放不需要单独维护；延伸加载也必须表现为「记录里多一步」。
- 证据：`web/src/stores/instance-reducer.ts`、`replay/extend.go`。

## ADR-010 撮合单一实现；OCO 同根止损优先

- 状态：已生效
- 背景：5 分钟 K 线看不出最高/最低谁先到；机器人若自造成交价，回测不可信。
- 决策：撮合只在 `instance.go`；同组一根最多成交一张；两张都触发时止损优先，与挂单顺序无关。
- 后果：回测口径偏保守但诚实；改挂单顺序不会翻转结果。
- 证据：`instance.go: ocoWinner`、`ocoRank`、`TestOCOStopWinsEvenWhenPlacedLast`。

## ADR-011 ATR 移到前端，EMA20 留在后端

- 状态：已生效（v0.23.0）
- 背景：ATR 的周期与平滑方式成为用户随手可调的显示参数，组合无穷，后端无法预算。
- 决策：ATR / Doji / ZigZag 在前端算（`chart/atr.ts`、`indicators.ts`）；EMA20 作为盘面事实仍由 Go 下发。
- 后果：README 中「`Bar.atr14`」的旧描述已失效；新指标先判断属于「盘面事实」还是「显示参数」。
- 证据：`replay/protocol.go:279`、`web/src/components/chart/atr.ts`。

## ADR-012 标注坐标只用「K 线时间 + 价格」

- 状态：已生效
- 背景：像素坐标在缩放、换窗口、换显示器后全错。
- 决策：后端只认时间与价格；越过最后一根的延伸用「锚点下标 + 偏移根数」。
- 后果：标注可跨设备一致；前端必须做同一套坐标变换。
- 证据：`replay/annotation.go`、`web/src/components/chart/scale.ts`。

## ADR-013 数据目录只读，状态目录分离

- 状态：已生效
- 背景：复盘是实验行为，不能污染采集到的行情。
- 决策：`data/` 只读（`ReadDayRows` / `Scan`）；实例存档写 `state/`，交易记录写 `recordings/`，
  样式模板写 `presets/`，原始帧写 `raw/`。
- 后果：删实例不影响行情；删记录不影响实例；备份边界清晰。
- 证据：`replay/persist.go`、`store/scan.go`。

## ADR-014 版本号单源（VERSION）+ 构建期资源

- 状态：已生效
- 背景：版本要同时出现在文件名、exe 属性、程序内三处，手填必漂。
- 决策：只改 `VERSION`；`build.cmd` 依次跑前端 → `go-winres` → `go build -ldflags -X main.version`。
- 后果：`.syso` 是编译期快照，不进仓库；`build.cmd` 必须纯 ASCII + CRLF。
- 证据：`VERSION`、`build.cmd`、`version_test.go`。

## 已废弃

| 决策 | 何时 | 说明 |
|---|---|---|
| 终端表格报表 | 已删除 | 与网页报告形成两套口径 |
| 手动开启/结束录制 | v0.18.0 | 改为按仓位自动开始/结束；旧消息仅作兼容别名 |
| 后端下发固定 ATR14 | v0.23.0 | 改为前端可调参数（见 ADR-011） |

## ADR-015 HTTP 接口统一返回 `{code,msg,data}`，包裹在过滤器层做

- 状态：已生效
- 背景：`/api/version` 最初直接返回业务对象；一旦加入异常、参数校验、404，各路径的 JSON 结构就会各不相同，
  前端每个调用点都得判断「这次拿到的是数据还是错误体」。
- 决策：Controller 只返回业务对象，外层由 `GlobalResponseAdvice`（`ResponseBodyAdvice`）统一包裹；
  异常由 `GlobalExceptionHandler`（`@RestControllerAdvice`）转成同一结构。
  业务异常一律 HTTP 200 + `code >= 10000`；协议层错误（400/404/405/415/500）HTTP 状态码照实返回，body 结构不变。
  `data` 字段单独标 `@JsonInclude(ALWAYS)`，抵消 `default-property-inclusion: non_null` 把 null 字段吞掉的行为。
- 后果：新增接口不会漏包，`Controller` 签名保持干净；`String` 与 `void` 返回值需要特殊处理；
  返回 `R` 的方法会被幂等放行，不会套两层。
- 证据：`api/R.java`、`api/ResultCode.java`、`web/GlobalResponseAdvice.java`、`web/GlobalExceptionHandler.java`、
  `ResponseEnvelopeTest`（成功/业务异常/500/400/404 五种结构一致）。

---

## 待决策

以下事项**尚未确认**，讨论时不得预设结论，也不得先写代码。

### P-001 后端是否迁移到 Java Spring Boot

- 需要先回答：迁移范围（全部服务 / 仅复盘服务 / 仅账户与考核）、时间表、团队维护能力。
- 前置条件：业务事实清单、黄金样本（真实 CSV + 期望成交/统计）、逐笔对账脚本、回滚方案。
- 验收：同一份行情与同一串操作，新旧实现产出逐笔一致的订单、成交、盈亏与统计。
- 风险：`replay` 是 150 个测试的单一应用包，一次性重写会丢失测试资产；建议按「先账户域、后撮合域」切。

### P-002 账户与考核域如何拆分

- 现状：账户计算与交易事实都挤在 `Instance`（`Position` + `RealizedPnl`），无账户对象。
- 待定：`Account` 是独立聚合还是 `Instance` 的父级；权益是实时算还是事件回放算；
  考核判定是纯函数还是状态机。
- 约束：无论怎么拆，撮合与盈亏仍只能有一份实现（见 ADR-010）。

### P-003 是否引入交易成本模型

- 影响：手续费/滑点一旦进入记账，nATR 面板的预告金额、全部历史统计、PF 通过率都会变。
- 待定：成本口径（固定/按手/按名义价值）、是否区分模拟与考核、历史数据是否重算。

### P-004 多实例是否共享账户

- 现状：每个实例独立记账，删实例即失去该实例的盈亏视图。
- 待定：账户与实例是 1:N 还是各自独立；跨标的、跨日如何汇总；记录页按什么维度聚合。

### P-005 是否拆分 `internal/replay`

- 现状：`instance.go` 超 1100 行，`replay` 包承担状态机、撮合、标注、记录、机器人、HTTP、WS。
- 待定：拆成 `replay/engine` + `replay/records` + `replay/transport` 是否值得，
  以「拆分后测试仍全绿且协议不变」为门槛。

新增决策时按同样格式追加（编号、状态、背景、决策、后果、证据），并在 `AGENTS.md` 的文档索引里保持可发现。
