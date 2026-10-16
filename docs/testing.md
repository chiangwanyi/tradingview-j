# 测试与排查

## 自动测试：全部离线，不启动任何浏览器

```bash
go test ./...
```

这一点是硬要求：**禁止用无头浏览器测试**（慢，而且浏览器进程中断这类问题在测试里看不见）。
替代方案是**用一个假 CDP 服务端扮演 Chrome**（`internal/wstest`）—— 它能应答
`Target.getTargets` / `Target.attachToTarget` / `Network.enable`，
然后把**真实抓包**当成 WebSocket 帧事件推下去：

```
internal/capture/capture_test.go
  ├─ 起一个假 CDP 服务端（HTTP /json/version + WebSocket 命令循环）
  ├─ 推 3 条真实帧（symbol_resolved / timescale_update / du 各一条，外加一条心跳）
  └─ 断言：CSV 真的写出来了、300 行、时间是明文、表头正确、
           原始帧留档存在、日志里确认了品种与周期
```

除了「Chrome 自己的行为」以外，从握手、分帧、抽 K 线、周期判定、品种识别到落盘、留档，
全部真跑了一遍。各层另有单元测试：

```bash
go test ./internal/tvproto/   # 分帧 / 抽 K 线 / 品种与序列绑定解析（含两份真实抓包回归）
go test ./internal/store/     # CSV 落盘 / 覆盖语义 / 重启后合并 / 坏行容错 / 只读扫描
go test ./internal/ingest/    # 周期反推 / 暂存补写 / 品种归属与串味防护
go test ./internal/cdp/       # 端点发现 / 命令与事件 / 超时 / 断线
go test ./internal/wsclient/  # 握手 / 大帧 / 分片 / ping-pong
go test ./internal/config/    # 配置生成 / 缺键补齐 / 非法值回退
go test ./internal/logx/      # 状态行原地刷新 / 中文按显示宽度擦除 / 重定向不留回车
go test .                     # 菜单报表排版（表头是中文、数据行是 ASCII，列宽必须一致）
```

### 只读扫描的测试

菜单选项 2 的统计逻辑有自己的测试（`internal/store/scan_test.go`）：造几个日文件，
断言根数、缺口、最大断档、按日期合计、坏行只跳自己、目录不存在只报说明、
`_symbols.json` 登记与否对标的显示的影响、重复行计入，以及 ——
**扫描前后把整个目录快照对比，证明它一个字节都没动**。

报表排版的测试（`report_test.go`）用「每行显示宽度必须相等」把口径钉死：
表头是中文（`日期`、`最大断档`）、数据行几乎全是 ASCII，
一旦有人把「按显示宽度补齐」换成 `fmt` 的 `%-12s` 或 `len()`，中文列会当场少算一半宽度、
整张表往左串行 —— 而那种歪斜在代码里看不出来，只有盯着终端才发现。

### 环境提示

本机跑 `internal/capture`（依赖 `net`/`tls`，产物较大）时，测试二进制偶尔会被
Windows 的 Application Control 策略拦下，报 `fork/exec ... An Application Control policy
has blocked this file`。这是策略对新生临时 exe 的拦截，与代码无关；换个临时目录即可绕开：

```bash
mkdir -p .tmpbuild && GOTMPDIR="$PWD/.tmpbuild" go test ./internal/capture/
```

（注意：`GOTMPDIR` 会连带影响 `t.TempDir()` 的位置，个别依赖临时目录路径的断言可能因此失败 ——
那种失败同样是环境问题，不是回归。）

## 回归样本

`testdata/` 下两份样本都是从**真实抓包**里裁出来的。

**`real-capture.ndjson`**（2026-10-16，`CME_MINI:MNQ1!` 5 分钟图）：
meta + 一条携带完整 300 根历史的 `timescale_update` + 一条 `du` + 一条心跳。
里面的期望值是用独立脚本统计出来的，**不是照抄被测代码的输出** ——
用被测代码生成期望值等于没测。

注意它里面有两个图表会话：那条 `du` 属于另一个会话（`cs_gaSTXIAFy41H`）且只有 1 根，
凑不齐两票，所以按规则一直暂存 —— 这正是「周期按会话分别反推」被钉住的地方。

**`mixed-symbols.ndjson`**：那次「品种认错」的回归样本。同一个页面上开着
`CME_MINI:MNQ1!` 的 5 分钟图，同时有 10 个别的品种在走报价（`NASDAQ:NDX` 被报价 5 次，
就是它把旧版本带跑偏的）。样本里有 `resolve_symbol` / `create_series` / `symbol_resolved`、
一条 300 根的 `timescale_update`、三条 `du`（有两条落在 NASDAQ:NDX 那条报价**之后**，
正是旧版本会写错目录的那几根），以及那条带 `pro_name` 的 NDX 报价。它锁住三件事：

1. 全部 K 线来自图表会话的 `sds_1`（300 个不同时间戳，独立脚本统计的值）；
2. 那条 NDX 报价**一根 K 线都不贡献**，也改不了图表品种；
3. 跑完整链路（假 CDP 服务端 → CSV）之后，数据目录里**只有一个**标的文件夹。

顺带把「按形状抽 K 线会不会误抽报价/指标」这件事用整段真实抓包验过了：
那次抓包的 655 帧里，能抽出 K 线的段共 211 个，**全部**是图表会话的 `du` / `timescale_update`，
`qsd`（含 57KB 的纳指报价）与 `st` 指标一根都没贡献。

## 人工联调：必须由你自己打开浏览器

自动测试覆盖不到「Chrome 的真实行为」，这一段只能人工做，步骤很短：

1. 打开 Chrome → `chrome://inspect/#remote-debugging` → 勾选 Allow remote debugging
2. 打开 TradingView 的图表页，切到 **5 分钟**周期
3. 双击 `tv-capture.exe`，在菜单里按 **1**（直接回车只是重画菜单，不会开始抓）
4. 观察窗口输出，应当依次出现：
   - `发现浏览器调试端点：port 9222 · 来源 DevToolsActivePort`
   - `CDP 通道已建立，开始等待 TradingView 图表页…`
   - Chrome 里点一次 **Allow**
   - `已接入图表页：…`（附带识别到的品种）
   - `[状态] 页[...] 帧 N(收N/发N) …` 这行会原地刷新
5. 在图表上**往回拖动**几次，或用回放模式跳几个日期
6. 到 `<数据目录>/<标的>/<今天>.csv` 看文件是否在长大
7. `Ctrl+C` 停下来，确认打印出「落盘完成」和本次写入统计，然后回到菜单
8. 在菜单里选 **2**，对着刚才那些文件核一下根数对不对（这是最快的一次自检）

## 排查要点

| 现象 | 原因与位置 |
|---|---|
| 菜单里按回车没反应 | 设计如此：回车 = 重画菜单（`menu.go`）。要开始抓就按 **1** |
| 抓完之后窗口直接关了 | 只有选 **3** 才退出（`menu.go` 的 `menu`）。若确实退出了，看上一段日志里的报错 |
| 一直打印「还没发现 Chrome 调试端点」 | 「前置条件」里那个开关没勾，或 Chrome 没开（`internal/cdp` 的端点发现） |
| 连上了但没弹 Allow 框、随后报「连接已关闭」 | 上一次的授权被拒了，重新连一次并在浏览器里点 Allow |
| 找到页面但提示「接不进这个页面」 | **该标签页上开着开发者工具** —— 一个标签页同时只能有一个调试会话，关掉 DevTools 即可 |
| 帧在涨但 CSV 不涨 | 图表周期不是 5 分钟（日志里会写「序列 xxx 的周期是 1h」），或状态行里 `周期` 显示「未定」（`internal/ingest` 的周期反推） |
| 状态行里 `暂存 N 根` 一直不降 | 那些 K 线所在的序列凑不齐两票、周期反推不出来（一般是页面上另一个会话/迷你图的零星数据），或者它们的会话里解析出多个品种而序列没有绑定。**它们不会落盘，也不影响主图采集** —— 这是刻意的（`internal/ingest`） |
| `品种` 显示的标的跟你正在看的图表不一致 | 先看日志里那句 `识别品种：…（会话 … · 品种键 …）` 对不对。实在不放心就把 `config.json` 的 `symbol` 填死（`internal/tvproto` 的归属判定） |
| CSV 文件被 Excel 占用导致写失败 | 日志会提示「下个周期重试」，关掉 Excel 即可（`internal/store` 的 `writeFileAtomic`） |
| 菜单里按 `Ctrl+C` 程序直接没了 | 回到菜单后 SIGINT 已经交还给系统（`main.go` 里 `stop()` 在刷盘之后才调用），此时 Ctrl+C 就是退出程序 |
| 「分析」里的 `重复N行` 亮了 | 说明日文件被手工改过或被别的工具合并过。正常写入（整体重写 + 原子改名）下同一时间戳不可能出现两次 —— 这是**报警**，不是统计（见 [design.md](design.md) 的「写入语义」） |
| 「分析」里的缺口看着很大 | 先看是不是休市造成：工作日 12 根 / 最大断档 1时5分、周六约 60 根都是正常形态（见 [design.md](design.md) 的附录） |

## 改代码时的注意

- **不要引入第三方依赖**：`go.mod` 目前没有 `require`，这是刻意的（见 [design.md](design.md)）。
- **不要让「分析」写任何东西**：统计路径必须走只读的 `store.Scan`，
  `store.Store` 一 Open 就会建目录、分配标的目录名。
- **不要让两处各写一份解析**：CSV 行格式只在 `store.readDayFile` 里定义一次。
- **`build.cmd` 必须是 CRLF 行尾**。本仓库 `core.autocrlf=true`，但那只在 checkout/commit
  时转换，**工具直接写盘不会被转**，所以用写文件的方式生成 `.cmd` 就会留下 LF ——
  cmd.exe 按字节偏移回读脚本，LF 会让它吃掉每行行首若干字符，报出一串
  「xxx 不是内部或外部命令」（实测把 `exit /b 1` 吃成 `it`、`pause` 吃成 `e`）。
  已用 `.gitattributes`（`*.cmd text eol=crlf`）钉死。改这个文件务必按 CRLF 写。
- **菜单只能有一个 stdin reader**：菜单读缓冲 reader、「按回车继续」又直接读 `os.Stdin` 的话，
  缓冲层里攒着的字符会被绕过去，表现成「按一次回车跳过两项」（`menu.go`）。
