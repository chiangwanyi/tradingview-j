# tradingview-j 项目长期笔记

## 这个项目是什么（2026-10-16 起是 Go 版）

通过 CDP 接入**用户本人已登录的 Chrome**，**被动**旁路抓 TradingView 图表页的 WebSocket 帧，
解出 5 分钟 K 线，写成 CSV。形态是**一个 Go 程序**：

- 产物 `tv-capture.exe`（根目录；`go build -o tv-capture.exe .` 或双击 `build.cmd`）。
- **入口是菜单式**：v1.1.0 起是 `1 启动抓取`/`2 分析已抓取的数据`/`3 退出`；
  **v1.2.0 起变成四项**：`1 启动抓取` / `2 分析已抓取的数据（终端）` / `3 网页版分析` / `4 退出`
  （退出键从 3 挪到 4，`q/quit/exit` 仍然可用）。抓取和网页服务都是 **Ctrl+C 停 → 回到菜单**。
- **抓取链路上没有任何 JavaScript**：只被动监听，不需要页面注入，也不需要扩展。
  唯一的 JS 是 v1.2.0 新增的菜单 3 网页版界面（`internal/webreport/static/`）：
  零构建 Vue 3 + Tailwind 浏览器版，两个库用 `//go:embed static` 内嵌进 exe ——
  仍然不需要 npm、不需要联网、产物仍然只有一个 exe。
- **网页版是日历视图（v1.3.0 起，之前是按日的表格）**：每个标的一张月历，
  格子中间的大数字 = 当天 K 线根数；点某一天在右侧出当天明细。
  「全部标的按日期合计」也是一张月历（当天合计根数 + 有几个标的）。详见下面「核心约定」。
- **配置只有 `config.json`**（exe 同目录），**不接受任何命令行参数** —— 使用方式是双击。
  相对路径（outDir/rawDir）相对 **exe 所在目录**展开，因为双击时 cwd 不可靠。
  `webPort`（默认 8765）是菜单 3 的监听端口，占用时自动换端口并**在终端里说明**。
- **零第三方依赖**，`go.mod` 里没有 require；WebSocket 客户端自写在 `internal/wsclient`。
- 结构：`main.go`（横幅+菜单分发）+ `menu.go`（菜单与输入）+ `analyze.go`（选项 2 的终端排版）
  + `web.go`（选项 3：起本地网页服务、开浏览器、等 Ctrl+C）
  + `internal/{config,cdp,wsclient,tvproto,ingest,store,rawlog,capture,logx,webreport}`
  （`store/scan.go` 是只读扫描；`webreport` = JSON 接口 + 内嵌前端）
  + `internal/wstest`（测试专用假 WS 服务端）
  + `testdata/`（两份真实抓包裁剪的回归样本：`real-capture.ndjson` 普通样本、
  `mixed-symbols.ndjson` 品种串味回归样本）。
- **文档分工（2026-10-16 起）**：`README.md` 只放项目介绍与上手（特性 / 前置条件 / 快速开始 /
  使用 / 配置 / 数据输出 / 项目结构 / FAQ / 注意事项），深度内容拆到
  `docs/design.md`（设计取舍 + 协议与实测事实附录 + 旧版对照）与
  `docs/testing.md`（离线测试 / 回归样本 / 人工联调 / 排查要点 / 改代码注意）。
  新增"为什么这么做"的内容写进 design.md，别往 README 里堆。
- `legacy/`（旧 Node 实现：网页工作台 + SQLite + 自动回拉 + 覆盖度报告，含旧 `data/`）
  **已于 2026-10-16 整体删除**（60 个文件 / 14MB，用户明确要求）。删除前确认过它全部在 HEAD 里，
  可完整取回：`git checkout ac9e35c -- legacy/`。
  Node 运行时的路径（本 Go 项目用不到）：
  `C:/Users/jiang/.workbuddy/binaries/node/versions/22.22.2-6/node.exe`

## 核心约定

- **只被动监听**：不拖图表、不刷新、不注入脚本、无自动回拉。风控优先，这是刻意的。
- **只收 5 分钟**（`intervalSeconds: 300`），其他周期的 series 整段跳过并记日志。
- **周期靠相邻时间戳的众数差反推**（帧里没有周期字段）；反推出来前 K 线**暂存不落盘**。
  反推**按（会话，序列）分别做**（`seriesKey{scope,name}`）：一个页面上可以有多个图表会话，
  它们的序列都叫 `sds_1`，混在一起投票会互相污染。
- **标的归属只认 `symbol_resolved`**，按「会话 + 序列」判定，绝不按序列名全局认：
  `config.symbol` >（`create_series` 的序列→品种键绑定 + `symbol_resolved` 的品种键→pro_name）
  > 该会话唯一品种 > 页面唯一品种 > URL `?symbol=` 猜测（日志会说明是猜的）> **暂存不落盘**。
  不知道写进哪个目录时绝不猜。
- **一个页面的 WebSocket 上跑的不只有图表数据**：侧边栏/自选/报价的 `qsd` 里同样带 `pro_name`
  且是别的品种（踩过：MNQ1! 的 K 线被写进 `NASDAQ_NDX/`）。所以旧的
  "在任意消息里搜 pro_name" 这种做法是 bug，已删除；`tvproto` 只暴露
  `SymbolResolved` / `SeriesBinding` / `MessageScope` / `QuoteNames`（最后一个只用于诊断）。
- **CSV 写入 = 整体重写**：内存按 `(标的,日期)` 聚合 `t→bar`，每 `flushSeconds` 秒
  用 tmp+rename 重写整个日文件。重复数据在结构上不可能出现，所以没有"是否已存在"判断。
  **启动时必须先把磁盘上当日文件读回内存**，否则重启后接着拖同一天会覆盖掉之前抓的。
- **时间列是本地时区明文 `yyyy-MM-dd HH:MM`（开盘时间）**，文件名 `yyyy-MM-dd.csv`。
  换机器/换时区会影响"哪一天"的划分。
- **写文件用 tmp+rename**；被 Excel 占用会失败，保持"脏"下个周期重试。
- **菜单/交互约定**：全程只有**一个** `bufio.Reader(os.Stdin)`（`menu.go` 的 `readLine`）。
  菜单读一个缓冲 reader、`waitEnter` 又直接读 `os.Stdin`，缓冲里的字符会被绕过去，
  表现成"按一次回车跳过两项"。`waitEnter` 在 stdin 非终端（重定向/后台）时直接返回，不空转。
  stdin EOF → 菜单直接退出，不转圈。
- **"分析"这条路径必须只读**：走 `store.Scan`，**不要**用 `store.Store`（它 Open 就建目录、
  会往 `_symbols.json` 里分配标的目录名）。测试用「扫描前后目录快照一致」钉住。
  **网页版（菜单 3）同理，而且走的就是同一个 `store.Scan`** —— 它只把结果换成 JSON。
- **菜单 3 网页版的两条硬约束**（v1.2.0 定下）：
  ① **统计量只在 Go 里算**，`app.js` 只做排版。若在 JS 里重算一遍口径，
  两个界面给出不同根数时用户无法裁决。反过来，**显示格式**（千分位 / `1时5分` / KB-MB）
  确实写了两份，靠 `internal/webreport/testdata/format.test.cjs` 逐条对着 `report_test.go`
  的期望值断言（`go test ./internal/webreport/` 会调 node 跑它，没 node 则 skip）。
  ② **服务只绑 `127.0.0.1`，且没有任何能改数据 / 触发动作的接口**（只有 `/api/analysis` + 内嵌静态）。
  ④ **日历视图（v1.3.0 起）的约定**：
  · **一个月份状态全局共用**（`month`），翻一次每个标的的日历和「全部标的合计」那张一起翻；
  不按标的各存一份，否则横向对比时要来回切两遍月份。
  · **格子里的数字一个都不是前端算的**。为了让日历能标红「这一天有异常」，
  给 `store.DateTotal` 加了 `Issues`（这一天有几个标的的文件带坏行/重复/读不了），
  `webreport` 透传成 `"issues"`。**不要**改成让 JS 扫一遍 `folders` 去凑 —— 那就是第二份口径。
  · 「只要异常 / 只要缺口」这两个开关在日历上语义是**高亮**（没命中的变淡、不隐藏），
  因为日历用「空着」表示「没有数据」，把格子藏掉会让人误以为那天没数据。
  · 格子的观感**全在 `style.css` 的 `.cal-*`**，一个 Tailwind 工具类都不用。原因：
    承载格子的模板是**运行期字符串** —— 放在 `index.html` 的 `<template id="tv-cal-tpl">` 里
    （**必须在 `#app` 之外**，放进去会被当成页面模板编译），`app.js` 的 `templateText()` 取它的
    `innerHTML` 注册成 `cal-grid` 组件（`innerHTML` 空了就克隆 `.content` 兜底）。
    Tailwind 浏览器版是扫 DOM 推导样式的，运行期才出现的类名有漏扫风险，漏了就是格子挤成一坨
    且**没有任何报错**。`.cal-row/.cal-col/.detail-col` 这些布局类同理写进了 CSS。
  · 日历的**日期算术在前端**（`monthOf/isMonth/addMonth/todayMonth/monthCells/weekday/monthsOf`），
    断言用固定日期（`2026-10-01` 是周四 → 前补 3 格、整月 35 格；闰年 2 月 29 天），
    **不要写依赖「今天是什么日子」的期望值**。`addMonth` 交给 `Date` 进位，不自己算跨年/闰月。
  · 表格时代的排序（点表头）已删；`sortFiles` 留着，用在「当天各标的」那张小表上。
  ③ 改 `internal/webreport/static/` 下任何文件都要**重新 `go build`** —— `//go:embed` 是编译期快照。
- **统计口径与落盘共用一份代码**：`store.readDayFile` / `parseRow` / `readIndexFile`。
  两边各写一份解析迟早会漂移，表现成"文件里 288 行、分析说 287 根"，用户没法判断信谁。
- **测试禁止无头浏览器**（用户明确要求：慢、且中断不可见）。替代 = 假 CDP 服务端。
  `go test ./...` 全离线可跑，人工联调由用户自己开浏览器做。
  **不想开浏览器又能查出「页面为什么全白」的便宜办法（2026-10-16 用通）**：
  在 node 里用 `vue.global.prod.js`（完整版，带运行时编译器）把模板编译一遍 ——
  `Vue.compile(tpl, {onError: e => {throw e}, onWarn: w => warns.push(w.message)})`
  能把模板语法错、指令写错当场报出来。两个必须踩到的坑：
  ① 要给一个极小的 `document` 替身（`createElement` 返回带 `innerHTML` getter/setter、
  `textContent`、`children[0].getAttribute` 的对象），编译器只用它解 HTML 实体；
  ② 必须 **`globalThis.Vue = Vue`** —— 生成的渲染函数里直接引用全局 `Vue`，
  不挂回全局就是 `ReferenceError: Vue is not defined`。
  再配一段正则把模板里引用的标识符（先剥掉字符串字面量与 `v-for` 别名）回查 `app.js`，
  就能确认没有引用不存在的方法/字段。脚本是一次性的，不进仓库、不进 `go test`。
- **本机智能应用控制（SAC）会拦 `go test ./internal/capture/` 与 `go run .`**（2026-10-16 定位）。
  报 `fork/exec ...: An Application Control policy has blocked this file`，右下角同时弹
  「此应用的一部分已被阻止」（截图为 `capture.test.exe`）。触发条件**只剩一条：由 `go.exe`
  亲自启动的那个二进制**；已逐项排除：路径（同一 exe 放 `%TEMP%\go-build998877\b001\`
  由脚本启动**正常**）、目录名与层级、写盘后时间窗口（0/5/20 秒后执行都正常）、
  文件内容与大小（**同一份二进制**换个启动者就能跑）。`store`/`tvproto`/`config`/`ingest`/
  `wsclient`/`cdp`/`logx` 和主包的测试**都不受影响**，`go vet` 也不受影响（只分析不执行）。
  SAC 内部为何区分加载者**没有查到确证，标为未知**。
  **旧笔记"换 `GOTMPDIR=$PWD/.tmpbuild` 即可绕开"是错的，已实测证伪**（该路径同样被拦，
  CodeIntegrity 事件日志里有记录）。SAC **没有单应用白名单** → 换目录、安全中心排除项、
  自签名统统无效（自签名不在 Trusted Root Program，SAC 不认）。两条出路：
  三条出路：① `go test -c -o <临时目录>` + `cd <包目录>` 手动启动（已跑通 capture 全部 3 个用例；
  `cd` 到包目录是必须的，但 IDE 测试按钮与直接敲的 `go test` 仍会被拦）；
  ② 切到「评估」——评估期不拦任何东西，且「评估 ↔ 打开」可逆（本机就是被系统从评估期自动切成
  强制的），缺点是会被系统再次自动升级回强制，属**临时缓解**；
  ③ 关掉 SAC —— **代价很大，见下一条**。
- **在本机「关闭 SAC」是单向的，不要赌**：本机确认框原文是「如果关闭智能应用控制，则无法重新
  打开或切换到评估模式，除非转到 Windows 恢复选项并重置此电脑」（2026-10-16 用户截图）。
  微软官方 FAQ 另有"近期更新允许在不全新安装的情况下重新启用"的说法，**与本机提示冲突**，
  两者无法在不越过这条不可逆线的条件下验证 → **一律以本机提示为准**。
  教训：不能把"查到的官方说法"当成"在用户这台机器上成立"，动手前应先让用户点开确认框看一眼
  （这次我就是先说了"可逆"，用户截图推翻，及时叫停）。
- **让 exe "受信任"以绕开 SAC：能，但对本项目的开发循环没用。** SAC 只认 ① 微软云信誉
  ② **受信任提供商**签发的证书。**自签名无效** —— Eric Lawrence（Chrome/Edge 安全团队，
  2026-04）明确写："signatures from certificates that chain to a root outside of Microsoft's
  program (e.g. one that chains to a root the user or admin trusted via certmgr.msc or Group Policy)
  will not be considered valid"；微软员工 Q&A 也说 "Self-signed certs will not work"。
  **网上"自签名 + 装到 certmgr 受信任根即可"的教程与官方口径冲突，不要采信。**
  要签就买（官方推荐 Microsoft Trusted Signing，原 Azure Code Signing；传统 OV 证书一年数千元，
  2023-06 起私钥须在硬件令牌/HSM）且要用 **RSA**（英文文档说不支持 ECC，丹麦语版说支持，
  口径不一致 → 用 RSA 最稳）。
  **关键：`go test` / `go run` 场景签名在原理上赶不上** —— go 自己编译临时二进制并立即执行，
  中间没有插入签名步骤的时机。签名只对**固定产物**（如 `tv-capture.exe`）且**发给别人**时有意义。
  另：网上"开开发人员模式可豁免 SAC"**不成立**（微软员工原话 "Developer Mode doesn't exempt you from it"）。

## 关键事实（实测，别忘）

- Chrome ≥ 136：给默认用户数据目录加 `--remote-debugging-port` 被**静默忽略**，只能走
  `chrome://inspect/#remote-debugging` 的开关（≥144）；每个浏览器会话要重勾，
  每次外部连接会弹一次 Allow 框，**不点就不通**。
- 新版 Chrome 只开放**浏览器级 WS**，`/json/version`、`/json/list` 一律 404。
  端口与 WS 路径从 `<User Data>/DevToolsActivePort` 读（第一行端口，第二行 `/devtools/browser/<uuid>`）。
  页面发现走 `Target.getTargets` + `Target.attachToTarget`(flatten)。
- **一个标签页同时只能有一个调试会话** → 该页开着 DevTools 时 attach 必失败（代码 1 分钟退避）。
- 抓包里**有** `symbol_resolved`（旧版注释说没有，是错的）：
  `{"m":"symbol_resolved","p":["cs_X","sds_sym_1",{…"pro_name":"CME_MINI:MNQ1!"…}]}`。
  配套的绑定信令 `{"m":"create_series","p":["cs_X","sds_1","s1","sds_sym_1","5",300,""]}`
  是**客户端发上去的**（所以上行帧也要扫，但只扫这一条）。数据消息的 `p[0]` 就是会话 id。
  `modify_series` 未在抓包中出现过 —— 代码按同形状试解析，属**推断**。
- 报价里的伪品种键形如 `={"adjustment":"splits",…,"symbol":"CME_MINI_DL:MNQ1!"}`，
  不是品种名；`CME_MINI:MNQ1!` 的报价里还夹着 `"underlying-symbol":"NASDAQ:NDX"`。
- 按形状抽 K 线**不会**误抽报价/指标：655 帧的真实抓包里 211 个含 K 线的段全部是
  图表会话的 `du`/`timescale_update`，`qsd`（含 57KB 那一条）与 `st` 一根都没贡献。
- 帧：`~m~<长度>~m~<负载>`；心跳两种形态都见过 —— 裸 `~h~1` 与 `~m~4~m~~h~1`。
  声明长度与实测不符约 3%（5305 段中 146 段）→ 对不上时必须退化为"切到下一个帧头之前"；
  **但"负载长度必须等于声明长度"不能省**，否则声明超长时会把后面几段一起吞掉。
- K 线形状 `{s:[{i,v:[t,o,h,l,c,v]}]}`，t 是 Unix 秒，series 名是它上一层的键名（`sds_1`…）。
  `st1`/`st2` 是指标（`st` 键）、`qsd` 是报价，都不会被误抽。
- `lbs.bar_close_time` **不是**收盘时间，是图表右侧可视边界。
- 单帧最大实测 247KB（WS 客户端必须支持 64 位长度与分片；写帧必须掩码）。
- 图表**按需**取数：可视范围走出缓存窗口才发 `request_more_data` → 拖图表就是填历史的手段。
- 期货休市（CME，UTC）：每日 21:00–22:00 结算，周六全天 + 周日 22:00 前。
  看起来像"丢数据"的正常形态：工作日日文件 276 根、缺口 12、最大断档 1时5分
  （就是那 1 小时结算）；周六日文件约 60 根（凌晨收市）。
- **CSV 的时间列是明文本地时间，所以"时间戳是否落在文件名日期之外"这类自检恒为 0** ——
  读回来用哪个时区解析，日期部分都还是原文那一天，判据自己是循环的。别写这种永远不响的检查
  （曾写过一版"越界根数"，发现恒 0 后删掉）。有效报警是**重复行计数**：写入是整体重写 + 原子改名，
  同一时间戳不可能重复，一旦不为 0 就说明文件被手工改过或被别的工具合并过。
- **`build.cmd` 必须是 CRLF 行尾**。本仓库 `core.autocrlf=true`，但那只在 checkout/commit
  时转换，**工具直接写盘不会被转**，所以用写文件的方式生成 .cmd 就会留下 LF ——
  cmd.exe 按字节偏移回读脚本，LF 会让它吃掉每行行首若干字符，报出一串
  「xxx 不是内部或外部命令」（实测把 `exit /b 1` 吃成 `it`、`pause` 吃成 `e`）。
  已用 `.gitattributes`（`*.cmd text eol=crlf`）钉死。改这个文件务必按 CRLF 写。

## 用户偏好

- 需要"先纠正认知误区，再给可执行结论"，并结合他自己工程的具体文件举例。
- 喜欢可视化图示配合讲解。
- 重视结论要有实测支撑，讨厌凭空推断 —— 汇报时如实标出哪些验证过、哪些还是猜测。
- 对**不可逆操作**敏感方向：声称"清除数据"时，我选择归档而非物理删除并明确告知，他没有反对。
