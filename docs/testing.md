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
go test ./internal/webreport/ # 网页版的 JSON 形状 / 静态资源 / 端口回退 / 只读
go test .                     # 菜单报表排版（表头是中文、数据行是 ASCII，列宽必须一致）
```

### 只读扫描的测试

菜单选项 2 的统计逻辑有自己的测试（`internal/store/scan_test.go`）：造几个日文件，
断言根数、首尾、断档区间的起止、每根 K 线一根、按日期合计、坏行只跳自己、目录不存在只报说明、
`_symbols.json` 登记与否对标的显示的影响、重复行计入，以及 ——
**扫描前后把整个目录快照对比，证明它一个字节都没动**。

其中两条是本机作息的实测结论，写错了会让日历天天误报，所以单独钉住：

- `TestExpectedBarsByWeekday` —— 「星期几应当有多少根」那张表（周一 216/204、周六 72/60、
  周日 0）以及「日期认不出来就不判」；
- `TestScanFlagsCrossSymbolMismatch` —— 跨标的比较**只比当天有数据的标的**：
  某个标的当天根本没这份文件（还没抓到）不能当成它抓了 0 根，否则每加一个新标的，
  它入库之前的每一天都会集体误报。

报表排版的测试（`report_test.go`）用「每行显示宽度必须相等」把口径钉死：
表头是中文（`日期`、`最大断档`）、数据行几乎全是 ASCII，
一旦有人把「按显示宽度补齐」换成 `fmt` 的 `%-12s` 或 `len()`，中文列会当场少算一半宽度、
整张表往左串行 —— 而那种歪斜在代码里看不出来，只有盯着终端才发现。

### 网页版分析（菜单 3）的测试

三层，全部离线：

1. **`report_test.go`** —— `Build` 是纯函数（只读入参、不碰磁盘、不看时钟），
   拿手工构造的 `store.ScanResult` 就能断言：合计、时间跨度、标的映射、日文件数字、
   断档区间、应有根数、取日期合计，以及 **notes / 读不了的错误原样透传**。
   其中有两条是刻意的：① **`Build` 一次都不许重算判定** —— 用例把根数改成 275 而
   `BarsOff` 保持 scanner 给的 false，透传出来就必须还是 false（谁在 Build 或前端里
   自己按 276 比一遍，这条会先红）；② 空结果必须序列化成 `[]` 而不是 `null`
   （前端直接 `.length`，`null` 会让模板当场报错），断档为空的那天 `gaps` 也必须是 `[]`。
2. **`server_test.go`** —— 起 `httptest`，真造一个数据目录（含一份 `_symbols.json`
   和一份 `.tmp` 干扰文件），然后：断言 `/api/analysis` 的数字与 `store.Scan` 一致
   （3 根 / 断档 1 处且缺 1 根 / `.tmp` 不算数据）；**断言响应里不再有 `missing`、`maxGap`**
   （那两个概念已经删掉，字段不该悄悄活着）；断言首页与四个静态资源都取得到；
   断言未知路径是 404；断言端口被占用时**换端口并置 `Moved`**；
   最后 —— **分析前后目录快照逐项对比**，证明网页版一个字节都没动。
3. **`frontend_test.go`** —— 让 node 跑 `testdata/format.test.cjs`：
   把 `app.js` 里 createApp 之前的那段纯函数切出来 eval，逐条断言
   `num` / `bytes` 与 **`report_test.go` 里那张期望值表完全一致**，
   外加判据的转述（`dayAnomaly` / `dayNotes` / `cellSub` / `expectText` / `countsText`）、
   排序（不原地改数组、同值按日期兜底）、告警文案，
   以及**日历的日期算术**（`monthCells` 铺格子、`addMonth` 跨年、`weekday`、
   `monthsOf` 去重倒序、闰年 2 月）。日历那几条刻意用固定日期算期望值
   （`2026-10-01` 是周四 → 前面补 3 格、整月 35 格），不依赖跑测试的机器上是什么日子。
   没有 node 就 `t.Skip` —— 这是交叉校验，不该变成所有人都必须装 node 的理由。
   手工跑：`node internal/webreport/testdata/format.test.cjs`。

> 前端还有一类问题这三层都测不到：**页面到底有没有渲染出来**。
> 实测踩过一次 —— `meta.ready` 这个标志忘了在拿到数据时置上，
> 接口 200、DOM 里能查到数据、「快照时间」也显示出来了，但整块内容被 `v-if` 挡住，
> 页面永远停在「正在读取…」。修法是不再另设标志，模板直接用 `rep` 本身判断。
> 这类只认「真浏览器里量出来的几何和 computed style」，
> 用 Edge/Chrome 的 `--headless=new --remote-debugging-port` + 一个几十行的 CDP 脚本
> （Node 22 自带 `WebSocket`，零安装）跑一次即可：断言栅格列数、字号、颜色、
> 日历格子数（7 的整数倍、当月天数为 31 / 30 / 29 / 28）、点某一天后右侧面板的出现、
> 筛选变淡、折叠、深色模式的真实效果。
> 这是**开发时的一次性验证**，不进 `go test`（那条线仍然禁止浏览器）。
>
> 不想开浏览器时，有一件事可以便宜地验：**模板本身能不能编译**。
> `vue.global.prod.js` 是完整版（带运行时编译器），在 node 里给它一个
> 只说 `createElement` / `querySelector` 的极小 `document` 替身，
> 就能把 `#app` 里的整页模板和 `index.html` 里那段日历组件模板各编译一遍 ——
> 模板语法错、指令写错、引用了不存在的方法，都会在这里当场报出来。
> 这一步既没被 `go test` 覆盖，也是「页面全白」最常见的来源。

### 本机环境：智能应用控制会拦 `go test` / `go run`

本机开着 **智能应用控制**（Smart App Control），它会把 `go.exe` 直接拉起的二进制判为
不受信任并拦下，报：

```
fork/exec C:\Users\...\Temp\go-build<n>\b001\capture.test.exe:
    An Application Control policy has blocked this file.
```

**影响范围**（2026-10-16 实测）：只有 `internal/capture` 的测试和 `go run .` 被拦；
`store` / `tvproto` / `config` / `ingest` / `wsclient` / `cdp` / `logx` 以及主包 `.` 的测试
**都正常通过**。`go vet ./...` 不受影响（只分析、不执行）。

**触发条件只有一条：由 `go.exe` 亲自启动的那个二进制。** 下面这些都逐项排除过：

| 猜过的原因 | 实测结果 |
|---|---|
| 文件在 `%TEMP%` 里所以被拦 | ❌ 同一个 exe 放进 `%TEMP%\go-build998877\b001\`，脚本启动**正常** |
| `go-build*` 目录名或层级的问题 | ❌ 同上，正常 |
| 新文件写盘后要等云端裁决 | ❌ 编译完 0 / 5 / 20 秒后执行**都正常** |
| 二进制内容（capture 链接了 net/tls，11.7MB） | ❌ **同一个文件**换个启动者就能跑 |
| **`go.exe` 作为启动者** | ✅ **100% 复现被拦** |

也就是说，同一份 `capture.test.exe`，`go.exe` 启动必被拦、脚本启动必通过。
SAC 内部为什么区分加载者**没有查到确证** —— 这一条标为未知，不要当成结论。

事件日志里的原始记录（策略 ID `{0283ac0f-fff1-49ae-ada1-8a933130cad6}` 就是 SAC 内置那条）：

```powershell
Get-WinEvent -LogName 'Microsoft-Windows-CodeIntegrity/Operational' -MaxEvents 200 |
  Where-Object { $_.Message -like '*go.exe*' } | Select-Object TimeCreated, Id, Message
```

状态可以在这儿确认（`1` = 强制开启，`2` = 评估期，`0` = 关闭）：

```powershell
Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' |
  Select-Object VerifiedAndReputablePolicyState, SAC_PreviousState, SAC_EnforcementReason
```

**「换个临时目录」为什么没用**：微软官方 FAQ 明确写了 SAC **没有单应用白名单**，
所以换 `GOTMPDIR`、加安全中心排除项、给二进制自签名统统无效（自签名证书不在
Trusted Root Program 里，SAC 不认）。本文档旧版本写过"换 `GOTMPDIR` 即可绕开"，
**已被实测证伪**：`GOTMPDIR=$PWD/.tmpbuild` 那次同样被拦，事件日志里有记录。

**处理方式一：关掉智能应用控制**（根治，但**本机提示为不可逆**）

Windows 安全中心 → 应用和浏览器控制 → 智能应用控制设置 → 关闭。

点「关闭」后本机弹出的确认框原文是：

> 如果关闭智能应用控制，则无法重新打开或切换到评估模式，
> 除非转到 Windows 恢复选项并重置此电脑。

即**在这台机器上这条路是单向的**，代价是以后想用回来得重置电脑（重装 Windows、
丢掉已安装的软件）。微软官方 FAQ 另有"近期更新允许在不全新安装的情况下重新启用"的说法，
与本机提示**相互矛盾**，且**无法在不越过这条不可逆线的条件下验证** —— 因此以本机提示为准，
不要拿官方 FAQ 去赌。关闭后 `go test` / `go run` / IDE 里的测试按钮确实都会恢复正常。

另外，**切到「评估」也是不拦任何东西的**（评估期只观察、不阻止），而且
「评估 ↔ 打开」之间是可逆的 —— 本机就是评估期结束后被系统自动切成强制模式的
（`SAC_PreviousState = 2` → `VerifiedAndReputablePolicyState = 1`）。
但它会被系统再次自动升级回强制，属**临时缓解**，不是根治。

**处理方式二：先编译、再由脚本启动**（不动系统设置，已实测跑通 capture 全部 3 个用例）

```bash
TMPD="$TEMP/tvtest"; mkdir -p "$TMPD"
go test -c -o "$TMPD/capture.test.exe" ./internal/capture/
(cd internal/capture && "$TMPD/capture.test.exe")
```

`cd` 到包目录是**必须**的 —— 测试样本用相对路径 `../../testdata/...` 读，
cwd 不对会全部报「打不开样本」。
局限：任何直接敲的 `go test` / `go run`，以及 IDE 的测试集成，仍走 `go.exe` 启动，
照样被拦；这条路只在"手动跑一遍"时管用。

**能不能让自己的 exe 变成"受信任"，从根上不再被拦？**

能，但**只对"编译流程可控的固定产物"有效，对本项目的开发循环基本没用**。

SAC 只认两条路：① 微软云信誉服务明确判定为安全；② 用**受信任提供商**签发的证书签名。
自己刚编译出来的二进制既没有信誉、也不可能自带签名，所以默认被拦。

**自签名不算。** 这条有明确依据 —— Eric Lawrence（Chrome/Edge 安全团队）2026-04 写得很直白：

> Signature verification in Smart App Control is based on the Code Integrity platform in Windows…
> your certificate chain chains to a public Certificate Authority root that is included in
> Microsoft's Trusted Root Certificates program. Signatures from certificates that chain to a root
> **outside** of Microsoft's program (**e.g. one that chains to a root the user or admin trusted via
> certmgr.msc or Group Policy**) will not be considered valid.

也就是说，网上那些"用 `New-SelfSignedCertificate` 签个自签名证书、再把根证书塞进
`certmgr.msc` 的受信任根就能骗过 SAC"的教程（本机搜到的论坛做法），**与微软官方口径直接冲突**：
微软文档写的是 "Code can be signed with any certificate, but Smart App Control only considers
certificates issued by **trusted providers**"，微软员工在开发者问答里的回答也是
"Self-signed certs **will not work**"。本文档采信官方口径，**不把自签名当可行方案**。

要签名就得买：微软官方现在推荐 **Microsoft Trusted Signing**（原 Azure Code Signing），
或传统 CA 的代码签名证书（OV 级别一年数千元，且 2023-06 起私钥必须在硬件令牌/HSM 上）。
证书要用 **RSA** —— 微软英文文档写 "does not currently support ECC"，丹麦语版页面却写已支持 ECC，
不同语言版本口径不一致，**用 RSA 最稳**。

**但对最痛的场景，签名在原理上就赶不上：**

| 场景 | 签名有用吗 |
|---|---|
| `go test` | ❌ go **自己**编译出临时二进制并**立即执行**，中间没有任何地方能插入签名步骤 |
| `go run` | ❌ 同上 |
| `tv-capture.exe`（固定产物、编译流程可控） | ✅ 能签，而且**只对"发给别人"有意义**（自己机器上它本来就能跑） |

结论：**想让别人双击 `tv-capture.exe` 时不被 SAC 拦，签名有意义；想让自己本机
`go test` 不弹窗，签名帮不上 —— 它赶不上那个时机。**

顺带排除一条流传的说法：**"开开发人员模式就能豁免 SAC"不成立** ——
微软员工在开发者问答里的原话是 "Developer Mode doesn't exempt you from it"。

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
| 「分析」里的缺口看着很大 | 那是期货每日结算休市，不是丢数据。网页版（菜单 3）已经没有「缺口 / 最大断档」这两列了，改用「这一天应当有多少根」判异常（见 [design.md](design.md) 的附录） |
| 网页上某一天**标红** | 两条原因：① 根数与该星期应有的根数不符（鼠标停在格子上会写「周二应当是 276 根」）；② 文件有坏行 / 重复行 / 读不了。点开那一天，明细里的「检查」会逐条列出来 |
| 网页上某一天**标黄** | 当天各标的的根数不完全相同。点「全部标的按日期合计」里那一天，能看到各标的的分布（`276×2 · 275×1`），不是多数派的那几行也标黄了 —— 美国节假日提前收市就会这样，不同品种的收市时刻本来就不一样 |
| 日历上满屏标红 | 先看是不是**星期几那张表**跟你的数据对不上（比如标的是 24 小时交易的品种，或者数据不是 5 分钟）。表在 `store.ExpectedBars`，页面的「统计口径」里也印着。**别在 `app.js` 里改** —— 判据只许有一份 |
| `go test` / `go run` 报 `An Application Control policy has blocked this file`，同时右下角弹「此应用的一部分已被阻止」 | 智能应用控制拦了 **`go.exe` 启动的**那个二进制，与代码无关。为什么只拦它、以及两种处理方式，见本文档「本机环境：智能应用控制会拦 go test / go run」一节 |

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
- **网页版不许有「算数字」的代码**：根数 / 断档区间 / 应有根数 / 异常判定只能在 Go 里算
  （`store.Scan`），日历上「这一天有几个标的带异常」「各标的根数一不一致」也一样 ——
  它们在 `store.ByDate` 里（`DateTotal.Issues` / `Mismatch` / `Majority`），
  不让 `app.js` 扫一遍 `folders` 去凑。**连带效应**：「周几应当有多少根」那张表也只在
  `store.ExpectedBars` 一处，页面通过 `meta.expectRule` 印出来，HTML 里不许再抄一份数字。
  `app.js` 只做排版与「判据的转述」（`dayAnomaly` 就是把 Go 给的 `anomaly` 读出来，
  它自己不知道 276 这个数字）。
  改了 `num` / `bytes` / 判据转述那几个函数或日历的日期算术（`monthCells` / `addMonth` /
  `weekday` / `monthsOf`），就必须同步改 `internal/webreport/testdata/format.test.cjs` 的期望值
  （其中格式化那张表是照抄 `report_test.go` 的），
  否则 `go test ./internal/webreport/` 会红 —— 这是刻意的，两边都改才算改对。
- **改了 `internal/webreport/static/` 下任何文件都要重新 `go build`**：`//go:embed` 是
  编译期快照，改完不重编，跑起来的还是旧页面 —— 表现是「明明改了，浏览器里没变」。
  想不开浏览器就确认模板没写坏，可以拿 `vue.global.prod.js` 在 node 里把模板编译一遍
  （语法错、指令错当场报错，能被 `onError` 抓到；但**少一个闭合标签它不报**，
  因为 Vue 的解析器会自己补 —— 那种情况要么目测标签配平，要么真开一次浏览器）。
  这个脚本是一次性的，不进仓库、不进 `go test`。实测两个必须踩到的坑：
  ① 要给一个极小的 `document` 替身，而且要**按 innerHTML 回出 `textContent` 和
  `children[0].getAttribute('foo')`**（Vue 只用它解 HTML 实体，回 null 会让编译器自己崩）；
  ② vendor 里是 IIFE 形态的 global 构建，`require` 拿不到那个 `var Vue`，
  得把源码包进 `new Function(src + ';return Vue;')()` 里取出来。
- **日历的格子观感走 `style.css` 的 `.cal-*`，别在那儿叠 Tailwind 工具类**：
  承载格子的模板是**运行期字符串**（`index.html` 里 `<template id="tv-cal-tpl">` 的内容，
  由 `app.js` 取出来注册成 `cal-grid` 组件），而 Tailwind 的浏览器版是扫 DOM 推导样式的，
  运行期才出现的类名有漏扫的风险 —— 漏了就是「格子挤成一坨」而没有任何报错。
  另外这个 `<template>` 必须留在 `#app` **外面**，放进去会被当成页面模板编译。
- **改完 `internal/webreport/static/` 下的任何文件都要重新 `go build`**：
  `//go:embed` 是**编译期**快照，不重新构建的话 exe 里还是旧页面，
  而浏览器这边看起来「我明明改了」（这种现象最费时间）。
- **网页版必须只读、只监听 `127.0.0.1`**：它和菜单 2 一样走 `store.Scan`；
  不要给它加任何能改数据或触发动作的接口（见 [design.md](design.md) 的「网页版分析」）。
- **不要在 Go 里重写一遍前端排版**：终端报表（`analyze.go`）和网页的排版是两套，
  但**统计量只有一份**。想让两边长得一样时，加的是「把同一个数字显示得更清楚」的约定，
  不是把终端的字符串塞进 JSON。
