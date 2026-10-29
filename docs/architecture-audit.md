# 架构审计（2026-10-29）

本文件记录**从代码和测试中核实到的现状**，用于架构重设计前的基线。
凡标「未实现」的，是代码里确实没有的东西；凡标「未验证」的，是本次没有实际执行的部分。

## 审计范围与方法

- 通读：`go.mod`、`VERSION`、`main.go`、`internal/**`（13 个包）、`web/package.json`、`web/src` 目录树、`README.md`、构建脚本。
- 实测：`go test ./...`（全绿）、包间 `import` 依赖方向扫描、`git log`。
- 未执行：`build.cmd` / `build.sh`（会生成 exe）、`web` 的 `build` / `lint`、真实 Chrome 抓取链路、`data/` 全量统计。

## 1. 技术栈

| 层 | 现状 | 证据 |
|---|---|---|
| 后端 | Go 1.26，**零第三方依赖**，单 exe | `go.mod` 仅 `module` + `go`，无 `require` |
| 抓取 | 自写 CDP 客户端 + 自写 WebSocket 客户端 | `internal/cdp`、`internal/wsclient`（支持 64 位长度、分片、掩码） |
| 协议解析 | 自写帧解析 + K 线抽取 + 标的名解析 | `internal/tvproto` |
| 存储 | 明文 CSV，按「标的目录 / 日期文件」组织 | `internal/store` |
| 本地服务 | 两个只绑 `127.0.0.1` 的 HTTP 服务 | `config.json`：`webPort=8765`、`replayPort=8790` |
| 实时通道 | 复盘服务自带 WebSocket（JSON 文本帧），与分析页共用端口 | `internal/replay/server.go`、`ws.go` |
| 前端 | Vue 3.5 + Pinia 4 + vue-router 5 + TypeScript 6 + Vite 8 + Tailwind 4（CSS-first，无 `tailwind.config.js`） | `web/package.json` |
| 图表 | K 线用 Canvas 自绘、标注用 SVG；ECharts 6 仅用于 `/records` 统计图（懒加载 chunk） | `web/src/components/chart/`、`records/` |
| 指标 | EMA20 由后端下发（`Bar.ema20`）；**ATR 与 Doji/ZigZag 由前端计算** | `protocol.go:279`、`chart/atr.ts` |
| 资源 | Windows exe 图标/版本信息由 `go-winres` 在构建期生成 | `build.cmd`、`winres/` |

## 2. 目录与分层

`main.go` 只有 `main()` 与 `version` 变量（`-ldflags -X main.version` 的注入点），
其余启动逻辑全在 `internal/cli`。

实测包依赖（不含 `_test.go`）：

```
main    -> cli
cli     -> capture config logx replay store webreport
capture -> cdp config ingest logx rawlog store tvproto wsclient
ingest  -> store tvproto
replay  -> store strategy wsclient
cdp     -> wsclient
webreport -> store
config / logx / rawlog / store / strategy / tvproto / wsclient -> （无内部依赖）
```

- **无反向依赖**：`webreport` 不 import `replay`，`replay` 不 import `cli`。
- `internal/wstest`（测试专用假 WebSocket 服务端）只被三个 `*_test.go` 引用，不进产物。
- `internal/` 刻意保持扁平：包数即职责数，没有 `pkg/`、`cmd/` 之类中间层。
- **残留**：`internal/zpa/` 是空目录（无文件，git 不跟踪），属于历史实验残留。

## 3. 运行时拓扑

```
程序启动 ──▶ 终端菜单（3 项）
   1 启动抓取 ──▶ 等 CDP 端点 ──▶ 等图表页 ──▶ 被动读 WS 帧
                     │                            │
                     │                     tvproto 抽 K 线 → ingest 反推周期 → store 写 CSV
                     └──▶ webreport :8765（只读扫描 CSV，页面每 2 秒轮询）
   2 复盘     ──▶ replay :8790（HTTP + /ws，托管 web/dist）
                     │
                     ├─ 读 CSV（只读 ReadDayRows）
                     ├─ 状态写 state/（实例存档）
                     └─ 交易记录写 recordings/
   3 退出
```

- 菜单只有 3 项，不是 4 项；「分析页」不是独立菜单项，跟着抓取自启自停。
- 程序**不接受命令行参数**，全部配置在 exe 同目录 `config.json`，相对路径相对 exe 所在目录。
- 两个服务都在 `Ctrl+C` 后回到菜单，不退出进程。

## 4. 已实现的功能

| 功能 | 状态 | 关键实现 |
|---|---|---|
| 被动抓取 5 分钟 K 线 → CSV | 已实现 | `internal/capture`、`ingest`、`store` |
| 周期反推（相邻时间戳众数差，按会话+序列投票） | 已实现 | `internal/ingest/decode.go` |
| 标的名解析（只认 `symbol_resolved`，未定则暂存不落盘） | 已实现 | `internal/tvproto` |
| 原始帧留档（NDJSON） | 已实现 | `internal/rawlog` |
| 抓取实时分析页（月历 / 根数 / 断档 / 异常日，只读） | 已实现 | `internal/webreport` + `static/` |
| 逐根回放、随机/开盘/最新锚点、跳下一天开盘 | 已实现 | `internal/replay/data.go`、`session.go` |
| 标注（趋势线/射线/矩形/箭头/斐波那契，模板） | 已实现 | `replay/annotation.go`、`templates.go` |
| 模拟下单（限价/止损/市价、OCO、拖动改价、一键退仓） | 已实现 | `replay/instance.go` |
| nATR 定手数 + 自动保护止损 | 已实现 | 前端 `lib/natr.ts` + 后端 `marketOrderWithStop` |
| 手势：Ctrl 吸附 OHLC、Shift 水平线、右键按方向定单型 | 已实现 | `chart/snap.ts`、`orders.ts` |
| 多人：一人操作、其余观看、接管/交还 | 已实现 | `replay/hub.go` |
| 语音讲解（按 K 线时间挂载，WAV） | 已实现 | `replay/voice.go` |
| 交易记录（开仓自动开始、平仓自动结束、事件流） | 已实现 | `replay/trade.go`、`recording.go` |
| 记录页（月历、KPI、六张图、标签、来源筛选） | 已实现 | `web/src/views/Records*.vue`、`lib/records.ts` |
| 回放末尾延伸加载 K 线 + 延伸段录音 | 已实现 | `replay/extend.go` |
| 策略机器人（目前仅 `caveman` 一个策略） | 已实现 | `internal/strategy`、`replay/bot_strategy.go` |

## 5. 未实现 / 缺口（重点）

| 缺口 | 说明 |
|---|---|
| **账户模型** | 没有账户、余额、权益、可用资金。盈亏只挂在单个 `Instance` 上（`Instance.RealizedPnl`） |
| **PF 考核规则** | 没有利润目标、最大回撤、日亏限制、最少交易日、通过/失败判定的任何代码 |
| **交易成本** | 无手续费、无佣金、无点差、无滑点；市价单直接按当前 K 线收盘价成交 |
| **跨实例/跨日账户** | 每个实例独立记账，删除实例即失去该实例的盈亏视图；没有「账户」把多实例汇总 |
| **回测/批量评估** | 只能交互式逐根跑；`research/` 下是独立的 Python 实验（不在主程序链路） |
| **前端自动化测试** | 无 `*.test.ts`，只有 `type-check` / `lint` / `build` |
| **Java / Spring Boot** | 仓库内不存在任何 `.java`、`pom.xml`、`gradle` 文件，迁移未开始 |

## 6. 文档漂移（README 与代码不一致）

| README 说法 | 实际 |
|---|---|
| 开头「当前 `0.27.0`」 | `VERSION` 是 `0.30.0`，`main.go` 兜底值同 |
| 「EMA20 与 ATR14 在 Go 里算好下发（`Bar.atr14`）」 | v0.23.0 起 ATR 已搬到前端，`Bar` 无 `atr14` 字段；见 `protocol.go:279`、`web/src/components/chart/atr.ts` |
| 「日历日最后一根（23:55）无条件平仓」写在通用契约里 | 仅对**策略机器人**生效（`bot_strategy.go: closeOutLocked` 只被 bot 调用），手动仓位不强平 |
| 根目录只应有一个 exe | 同时存在 `tv-capture-v0.30.0.exe` 与 `tv-capture-v0.31.0.exe`，而 `VERSION` 是 `0.30.0` |

## 7. 技术债与风险

1. **`Instance` 是上帝对象**：头寸、订单、成交、标注、语音、记录器、机器人协程、游标、日文件索引全在一个结构体里（`instance.go:37`）。加「账户」或「考核」时若无边界，会继续膨胀。
2. **`internal/replay` 是应用单体**：150 个测试函数集中在此包，单文件职责偏重（`instance.go` 超过 1100 行）。
3. **协议双份手写**：`replay/protocol.go` 与 `web/src/types/protocol.ts` 靠人同步，字段名不一致不报错，只表现成「某个面板空白」。
4. **持久化格式无版本号**：`state/*.json`、`recordings/*.json` 靠 `omitempty` 与读取时兜底保持老档可读，没有显式 schema 版本。
5. **统计口径分散风险**：分析页统计在 `store.Scan`、交易统计在 `replay/trade.go`、前端有格式化函数；目前边界清楚，但新增报表时容易在 JS 里重算。
6. **平台绑定**：实际只在 Windows 上跑过；`.syso` 只链 windows/amd64。

## 8. 本次未验证

- 真实 Chrome + TradingView 的完整抓取链路（需人工：开远程调试 → 图表页 → 菜单 1 → Allow → 拖动图表）。
- `build.cmd` 三阶段是否仍能一次通过（未执行，会覆盖根目录 exe）。
- 前端 `npm run build` 产物与当前源码是否一致（`web/dist` 存在但未重建）。
