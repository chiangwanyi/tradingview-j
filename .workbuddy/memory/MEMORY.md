# tradingview-j 项目长期笔记

（2026-10-16 压缩重写：合并重复、删掉已失效的过程描述，事实与约定均保留。）

## 项目是什么

用 CDP 接入**用户本人已登录的 Chrome**，**被动**旁路抓 TradingView 图表页的 WebSocket 帧，
解出 5 分钟 K 线写成 CSV。**一个 Go 程序**，产物是**根目录**的 `tv-capture-v<版本>.exe`
（就地构建，**没有 dist 目录**；构建 = 双击 `build.cmd`，版本号机制见下面单独一节）。
**零第三方依赖**：`go.mod` 无 require，WS 客户端自写在 `internal/wsclient`
（`go-winres` 只是构建期工具，不进程序）。

- **菜单式入口**：`1 启动抓取` / `2 分析已抓取的数据（终端）` / `3 网页版分析` / `4 退出`
  （`q/quit/exit` 也可退出）。抓取与网页服务都是 **Ctrl+C 停 → 回到菜单**。
- **抓取链路上没有一行 JavaScript**；唯一的 JS 是菜单 3 的界面
  （`internal/webreport/static/`，零构建 Vue 3 + Tailwind 浏览器版，`//go:embed static`
  内嵌进 exe → 不需要 npm、不需要联网、产物仍只有一个 exe。套路见用户级 skill
  `vue-tailwind-nobuild-webapp`）。
- **配置只有 `config.json`**（exe 同目录），**不接受任何命令行参数**（使用方式是双击）。
  相对路径（outDir/rawDir）相对 **exe 所在目录**展开，因为双击时 cwd 不可靠。
  `webPort`（默认 8765）占用时自动换端口并**在终端里说明**。
- 结构：`main.go`（横幅+菜单分发）+ `menu.go` + `analyze.go`（选项 2 排版）
  + `web.go`（选项 3：起服务、开浏览器、等 Ctrl+C）
  + `internal/{config,cdp,wsclient,tvproto,ingest,store,rawlog,capture,logx,webreport}`
  + `internal/wstest`（测试用假 WS 服务端）+ `testdata/`（真实抓包裁剪的回归样本）。
- **文档分工**：`README.md` 只放介绍与上手；深度内容进 `docs/design.md`（设计取舍 + 协议实测附录）
  与 `docs/testing.md`（离线测试 / 回归样本 / 排查要点 / 改代码注意）。
  「为什么这么做」写 design.md，别往 README 堆。
- `legacy/`（旧 Node 实现：网页工作台 + SQLite + 自动回拉）**2026-10-16 整体删除**（60 文件/14MB），
  全在 HEAD 里，可取回：`git checkout ac9e35c -- legacy/`。

## 版本号（2026-10-16 起，当前 0.1.0）

- **唯一来源 = 根目录 `VERSION`**（CRLF，一行 `0.1.0`）。**升版本只改它，然后必须重跑
  `build.cmd`** —— 产物名、exe 文件属性、程序内显示三处全部由它推导。用户口径：对外从
  0.1.0 起，之前的 1.4.0 算内部迭代号（不是要继承的历史版本）。
- `build.cmd` 两步：① `set /p VER=<VERSION`；② `go-winres make --in winres\winres.json
  --out rsrc --arch amd64 --file-version=%VER% --product-version=%VER%`；③ `go build
  -ldflags "-X main.version=%VER%" -o "tv-capture-v%VER%.exe" .`（**就地**，无 dist）。
  `winres/winres.json` 里**不许写版本号**（会被命令行覆盖 → 留个永远不生效、又像真值的数字）。
  `winres` 的 JSON **不支持注释**，顶层写 `_comment` 会直接报 unmarshal 错。
- `version_test.go`（根包）钉五个落点：VERSION 一行三段数字 / `main.go` 的兜底字面量一致 /
  build.cmd 关键字齐全（含 `-o "tv-capture-v%VER%.exe"`）且是纯 ASCII + CRLF /
  build.cmd 命令里**不许出现 `dist`、`copy`、`mkdir`**（`TestBuildCmdKeepsExeInRootDir`，
  只看非 rem 行）/ winres.json 里无版本键。`go test .` 会拦这些漂移。
- 落点：`tv-capture-v0.1.0.exe` 文件名、exe 属性（`(Get-Item *.exe).VersionInfo`）、
  启动横幅、网页版页脚与 `/api/analysis` 的 `meta.version`。
- **产物就地放在根目录**（用户 2026-10-16 22:30 拍板，此前试过 `dist\` 又退回）：
  因为 outDir/rawDir/config.json 全按 **exe 所在目录**解析，「搬个目录 = 换一份数据」——
  dist 里的 exe 看不到根目录那 3268 份 CSV，还得每次构建拷一份 config.json 过去。
  现在 exe 与 config.json / data / raw 并排，双击即用。（被否掉的理由记在 design.md。）
- `go-winres` 已装在 `C:\Users\jiang\go\bin`（本机 PATH 里已有）。`build.cmd` 没装时会提示
  `go install github.com/tc-hib/go-winres@latest`。
- `/rsrc_windows_*.syso` 已 gitignore（编译期快照，每次构建重生成）。

## 核心约定（改代码必须守）

- **只被动监听**：不拖图表、不刷新、不注入脚本、无自动回拉。风控优先，这是刻意的。
- **只收 5 分钟**（`intervalSeconds: 300`），其他周期的 series 整段跳过并记日志。
- **周期靠相邻时间戳的众数差反推**（帧里没有周期字段）；反推出来前 K 线**暂存不落盘**。
  反推**按（会话，序列）分别做**（`seriesKey{scope,name}`）：一个页面可有多个图表会话，
  序列都叫 `sds_1`，混在一起投票会互相污染。
- **标的归属只认 `symbol_resolved`**，按「会话 + 序列」判，绝不按序列名全局认：
  `config.symbol` >（`create_series` 的序列→品种键绑定 + `symbol_resolved` 的品种键→pro_name）
  > 该会话唯一品种 > 页面唯一品种 > URL `?symbol=` 猜测（日志会说明是猜的）> **暂存不落盘**。
  不知道写进哪个目录时绝不猜。
- **一个页面的 WebSocket 上跑的不只有图表数据**：侧边栏/自选/报价的 `qsd` 同样带 `pro_name`
  且是别的品种（踩过：MNQ1! 的 K 线被写进 `NASDAQ_NDX/`）。旧的「在任意消息里搜 pro_name」
  是 bug，已删；`tvproto` 只暴露 `SymbolResolved` / `SeriesBinding` / `MessageScope` /
  `QuoteNames`（后者只用于诊断）。
- **CSV 写入 = 整体重写**：内存按 `(标的,日期)` 聚合 `t→bar`，每 `flushSeconds` 秒用 tmp+rename
  重写整个日文件。重复数据在结构上不可能出现，所以没有「是否已存在」判断。
  **启动时必须先把磁盘上当日文件读回内存**，否则重启后接着拖同一天会覆盖掉之前抓的。
- **时间列是本地时区明文 `yyyy-MM-dd HH:MM`（开盘时间）**，文件名 `yyyy-MM-dd.csv`。
  换机器/换时区会影响「哪一天」的划分。被 Excel 占用会写失败，保持「脏」下个周期重试。
- **菜单/交互约定**：全程只有**一个** `bufio.Reader(os.Stdin)`（`menu.go` 的 `readLine`）。
  菜单读缓冲 reader、`waitEnter` 又直接读 `os.Stdin`，缓冲里的字符会被绕过去，
  表现成「按一次回车跳过两项」。`waitEnter` 在 stdin 非终端（重定向/后台）时直接返回，
  不空转；stdin EOF → 菜单直接退出，不转圈。
- **「分析」这条路径必须只读**：走 `store.Scan`，**不要**用 `store.Store`（它 Open 就建目录、
  会往 `_symbols.json` 里分配标的目录名）。测试用「扫描前后目录快照一致」钉住。
  **网页版（菜单 3）走的就是同一个 `store.Scan`**，只是把结果换成 JSON。
- **菜单 3 的硬约束 ①**：**统计量只在 Go 里算**，`app.js` 只做排版（两份口径会让用户无法裁决）。
  例外只有**显示格式**（千分位 / `1时5分` / KB-MB）确实写了两份，靠
  `internal/webreport/testdata/format.test.cjs` 逐条对着 `report_test.go` 的期望值断言
  （`go test ./internal/webreport/` 会调 node 跑它，没 node 则 skip）。
- **硬约束 ②**：服务只绑 `127.0.0.1`，且**没有任何能改数据 / 触发动作的接口**
  （只有 `/api/analysis` + 内嵌静态）。
- **硬约束 ③**：改 `internal/webreport/static/` 下任何文件都要**重新 `go build`**——
  `//go:embed` 是编译期快照。
- **硬约束 ④（日历视图，v1.3.0 起）**：
  · **一个月份状态全局共用**（`month`），翻一次每个标的的日历和「全部标的合计」一起翻。
  · **格子里的数字一个都不是前端算的**。为让日历能标红「异常日」，`store.DateTotal` 有 `Issues`
    （这天有几个标的的文件带坏行/重复/读不了），`webreport` 透传成 `"issues"`。
    **不要**改成让 JS 扫 `folders` 去凑 —— 那就是第二份口径。
  · 「高亮异常日 / 高亮数量不一致日」在日历上是**高亮**（没命中的变淡、不隐藏），
    因为「空着」表示「没有数据」，藏格子会让人以为那天没数据。
  · 格子观感**全在 `style.css` 的 `.cal-*`**，一个 Tailwind 工具类都不用。原因：承载格子的模板是
    **运行期字符串**，放在 `index.html` 的 `<template id="tv-cal-tpl">`（**必须在 `#app` 之外**，
    放进去会被当页面模板编译），`app.js` 的 `templateText()` 取 `innerHTML` 注册成 `cal-grid`
    组件（`innerHTML` 空了就克隆 `.content` 兜底）。Tailwind 浏览器版靠扫 DOM 推导样式，
    运行期才出现的类名有漏扫风险，漏了就是格子挤成一坨且**没有任何报错**。
  · 日历日期算术在前端（`monthOf/isMonth/addMonth/todayMonth/monthCells/weekday/monthsOf`），
    断言用固定日期（`2026-10-01` 周四 → 前补 3 格、整月 35 格；闰年 2 月 29 天），
    **不要写依赖「今天是什么日子」的期望值**；`addMonth` 交给 `Date` 进位。
  · 表格时代的排序（点表头）已删；`sortFiles` 留着，用在「当天各标的」那张小表上。
- **硬约束 ⑤（判定口径，v1.4.0 起）**：
  · **「缺口」「最大断档」已从网页版删掉**（天天出现，看不出东西）。换成
    `store.Gap{StartT,EndT,Bars}` = **缺失那几根本身的应当时刻**，
    `EndT = StartT + (Bars-1)*step`（不从后一根倒推，脏数据下会算出 EndT < StartT）。
    实测 2451 份日文件：1604 份正好 1 处、4 份 2 处、843 份没有。
    `store` 里的 `Missing/Span/MaxGap` **还在**（终端菜单 2 的表仍用），只是不进 JSON。
  · **「异常日」= 根数不在该星期几应有的取值里，或文件有坏行/重复行/读不了**。
    取值表只在 `store.ExpectedBars`：周一 `{216,204}`、周二~周五 `{276}`、周六 `{72,60}`、
    周日 `{0}`。**周一和周六必须各两个值**——美国夏令时/冬令时让本地时刻挪 1 小时。
    实测 817 天**零误报**（周一的 204 全在冬令时），被判不符的 36 天全是美国节假日。
    表经 `meta.expectRule` 送到页面印出来，**HTML 里不许再抄一份数字**。
  · **「数量不一致」= 同一天各标的根数不完全相同**（`store.markMismatch`）。
    **只比当天有数据的标的**：某标的当天没这份文件不能当 0 根，否则每加一个新标的，
    它入库之前的每一天都会集体亮黄。实测 25/817 天（MGC 节假日提前收市）。
- **统计口径与落盘共用一份代码**：`store.readDayFile` / `parseRow` / `readIndexFile`。
  各写一份迟早漂移，表现成「文件里 288 行、分析说 287 根」，用户没法判断信谁。
- **测试禁止无头浏览器**（用户明确要求：慢、且中断不可见）。替代 = 假 CDP 服务端，
  `go test ./...` 全离线可跑，人工联调用户自己开浏览器。
  **不开浏览器也能查出「页面全白」的便宜办法（已用通）**：node 里用
  `vue.global.prod.js`（完整版，带运行时编译器）编译模板 ——
  `Vue.compile(tpl,{onError:e=>{throw e},onWarn:w=>warns.push(w.message)})` 能把模板语法错、
  指令写错当场报出来。两个坑：① 需要极小的 `document` 替身（`createElement` 返回带 `innerHTML`
  getter/setter、`textContent`、`children[0].getAttribute` 的对象，编译器只用它解 HTML 实体）；
  ② 必须 `globalThis.Vue = Vue`，生成的渲染函数直接引用全局 `Vue`。
  再配正则把模板里引用的标识符（先剥字符串字面量与 `v-for` 别名）回查 `app.js` 即可。
  脚本是一次性的，不进仓库、不进 `go test`。

## 本机环境：SAC、签名、go test 的坑

> **现状（2026-10-16 22:20 实测）**：`HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy` 的
> `VerifiedAndReputablePolicyState = 0`（按常见口径即「关闭」），
> `go test ./internal/capture/` 正常通过。下面这段拦截现象是当天早些时候的记录，
> **先留着当备用排查线索，别当成现在的常态**。

- **智能应用控制（SAC）会拦 `go test ./internal/capture/` 与 `go run .`**。报
  `fork/exec ...: An Application Control policy has blocked this file`，同时弹「此应用的一部分已被阻止」。
  触发条件**只剩一条：由 `go.exe` 亲自启动的那个二进制**。已逐项排除：路径（同一 exe 放
  `%TEMP%\go-build998877\b001\` 由脚本启动**正常**）、目录名与层级、写盘后时间窗口、
  文件内容与大小（**同一份二进制**换个启动者就能跑）。`store`/`tvproto`/`config`/`ingest`/
  `wsclient`/`cdp`/`logx` 和主包的测试**都不受影响**，`go vet` 也不受影响。SAC 内部为何区分加载者
  **未知**。旧笔记「换 `GOTMPDIR=$PWD/.tmpbuild` 可绕开」**已实测证伪**（同样被拦，
  CodeIntegrity 日志有记录）。SAC **没有单应用白名单** → 换目录、安全中心排除项、自签名全无效。
  三条出路：① `go test -c -o <临时目录>` + `cd <包目录>` 手动启动（已跑通 capture 全部 3 个用例；
  IDE 测试按钮与直接敲的 `go test` 仍会被拦）；② 切到「评估」（可逆，但会被系统再次自动升级回
  强制，属临时缓解）；③ 关掉 SAC —— 代价很大，见下条。
- **本机「关闭 SAC」是单向的，不要赌**：本机确认框原文是「如果关闭智能应用控制，则无法重新打开
  或切换到评估模式，除非转到 Windows 恢复选项并重置此电脑」（用户截图）。微软官方 FAQ 另有
  「近期更新允许不全新安装即可重新启用」的说法，**与本机提示冲突**，无法在不越过不可逆线的条件下
  验证 → **一律以本机提示为准**。教训：不能把「查到的官方说法」当成「在这台机器上成立」，
  动手前先让用户点开确认框看一眼（这次就是我先说「可逆」，用户截图推翻）。
- **给 exe 签名以绕开 SAC：能，但对开发循环没用。** SAC 只认 ① 微软云信誉 ② **受信任提供商**
  签发的证书。**自签名无效**（Eric Lawrence 明确写：链到微软程序之外根的签名不算有效；
  微软员工也说 "Self-signed certs will not work"）。网上「自签名 + 装 certmgr 受信任根即可」
  的教程与官方口径冲突，**不要采信**。要签就买（推荐 Microsoft Trusted Signing；传统 OV 一年数千元，
  2023-06 起私钥须在硬件令牌/HSM），用 **RSA**（英文文档说不支持 ECC，丹麦语版说支持 → 用 RSA 最稳）。
  **`go test` / `go run` 场景签名在原理上赶不上**：go 自己编译临时二进制并立即执行，中间没有插入
  签名的时机。签名只对**固定产物**（如 `tv-capture.exe`）且**发给别人**时有意义。
  另：「开开发人员模式可豁免 SAC」**不成立**。

## 关键事实（实测，别忘）

- Chrome ≥ 136：给默认用户数据目录加 `--remote-debugging-port` 被**静默忽略**，只能走
  `chrome://inspect/#remote-debugging` 的开关（≥144）；每个浏览器会话要重勾，每次外部连接弹一次
  Allow 框，**不点就不通**。
- 新版 Chrome 只开放**浏览器级 WS**，`/json/version`、`/json/list` 一律 404。端口与 WS 路径从
  `<User Data>/DevToolsActivePort` 读（第一行端口，第二行 `/devtools/browser/<uuid>`）。
  页面发现走 `Target.getTargets` + `Target.attachToTarget`(flatten)。
- **一个标签页同时只能有一个调试会话** → 该页开着 DevTools 时 attach 必失败（代码 1 分钟退避）。
- 抓包里**有** `symbol_resolved`：`{"m":"symbol_resolved","p":["cs_X","sds_sym_1",{…"pro_name":"CME_MINI:MNQ1!"…}]}`。
  配套绑定信令 `{"m":"create_series","p":["cs_X","sds_1","s1","sds_sym_1","5",300,""]}` 是**客户端发上去的**
  （所以上行帧也要扫，但只扫这一条）。数据消息的 `p[0]` 就是会话 id。`modify_series` 未在抓包中出现过
  —— 代码按同形状试解析，属**推断**。
- 报价里的伪品种键形如 `={"adjustment":"splits",…,"symbol":"CME_MINI_DL:MNQ1!"}`，不是品种名；
  `CME_MINI:MNQ1!` 的报价里还夹着 `"underlying-symbol":"NASDAQ:NDX"`。
- 按形状抽 K 线**不会**误抽报价/指标：655 帧真实抓包里 211 个含 K 线的段全部是图表会话的
  `du`/`timescale_update`，`qsd`（含 57KB 那条）与 `st` 一根都没贡献。
- 帧：`~m~<长度>~m~<负载>`；心跳两种形态都见过 —— 裸 `~h~1` 与 `~m~4~m~~h~1`。声明长度与实测不符约
  3%（5305 段中 146 段）→ 对不上时必须退化为「切到下一个帧头之前」；**但「负载长度必须等于声明长度」
  不能省**，否则声明超长时会把后面几段一起吞掉。
- K 线形状 `{s:[{i,v:[t,o,h,l,c,v]}]}`，t 是 Unix 秒，series 名是它上一层的键名（`sds_1`…）。
  `st1`/`st2` 是指标（`st` 键）、`qsd` 是报价，都不会被误抽。
- `lbs.bar_close_time` **不是**收盘时间，是图表右侧可视边界。单帧最大实测 247KB
  （WS 客户端必须支持 64 位长度与分片；写帧必须掩码）。图表**按需**取数：可视范围走出缓存窗口才发
  `request_more_data` → 拖图表就是填历史的手段。
- 期货休市（CME，UTC）：每日 21:00–22:00 结算，周六全天 + 周日 22:00 前。看起来像「丢数据」的正常形态：
  工作日日文件 276 根，其中固定一段断档**本地 05:00 → 05:55（缺 12 根）**就是那 1 小时结算；
  周六约 60 / 72 根（凌晨收市）。「这天该有多少根」的表见「核心约定 ⑤」。
- **CSV 时间列是明文本地时间，所以「时间戳是否落在文件名日期之外」这类自检恒为 0** ——
  读回来用哪个时区解析，日期部分都还是原文那一天，判据自己是循环的，别写这种永远不响的检查
  （曾写过一版「越界根数」，恒 0 后删掉）。有效报警是**重复行计数**：写入是整体重写 + 原子改名，
  同一时间戳不可能重复，一旦不为 0 就是文件被手工改过或被别的工具合并过。
- **`build.cmd` 必须同时是「纯 ASCII」和「CRLF」**，两条都是承重的（2026-10-16 实测补齐）：
  · **ASCII**：cmd.exe 回读批处理脚本按**字节偏移**定位每一行，多字节字符（中文）会让偏移
    和解码出来的字符错位，症状是**一行的尾段被单独当命令执行** —— 报
    `'。' is not recognized as an internal or external command`，而且相邻的 `echo` 整行消失
    （**输出的中文本身还是对的**，所以很容易误判成「显示问题」）。
    实测：改造前脚本 237 个非 ASCII 字节、一直正常；加入版本号机制后涨到 1071 个字节，
    跑到一半就炸。→ **这个文件里的日志一律写英文，中文留给程序自己打**。
  · **CRLF**：本仓库 `core.autocrlf=true` 只在 checkout/commit 时转换，**工具直接写盘不会被转**，
    用写文件方式生成 .cmd 就会留下 LF —— cmd.exe 假设每个换行占 2 字节，逐行累积错位、
    每行行首被吃掉（实测把 `exit /b 1` 吃成 `it`、`pause` 吃成 `e`）。
    `.gitattributes`（`*.cmd text eol=crlf`）只管 git 侧，管不了工具写盘。
  · 两条由 `version_test.go` 的 `TestBuildCmdIsAsciiAndCRLF` 断言（会报第几行哪个字节）。
  · **教训：`od -c build.cmd | head -1` 这种「只看第一行」的检查会骗人** —— 那次第一行恰好
    是 CRLF，后面全是 LF 没看出来。要整文件核：
    `python -c "d=open('build.cmd','rb').read(); print(sum(1 for b in d if b>0x7f), d.count(b'\n')-d.count(b'\r\n'))"`
    两个数都得是 0。

## 用户偏好

- 需要「先纠正认知误区，再给可执行结论」，并结合他自己工程的具体文件举例；喜欢可视化图示配合讲解。
- 重视结论要有实测支撑，讨厌凭空推断 —— 汇报时如实标出哪些验证过、哪些还是猜测。
- 对**不可逆操作**敏感：声称「清除数据」时，选择归档而非物理删除并明确告知，他没有反对。
