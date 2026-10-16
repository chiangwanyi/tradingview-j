# tradingview-j 项目长期笔记

> 2026-10-16 二次压缩：合并同义条目、删掉已被推翻的排查线，只留仍生效的事实与约定。

## 项目是什么

用 CDP 接入**用户本人已登录的 Chrome**，**被动**旁路抓 TradingView 图表页的 WebSocket 帧，
解出 5 分钟 K 线写成 CSV。**一个 Go 程序、零第三方依赖**（`go.mod` 无 require；WS 客户端自写在
`internal/wsclient`；`go-winres` 只是构建期工具）。产物是**根目录**的 `tv-capture-v<版本>.exe`
（就地构建，**没有 dist 目录**；构建 = 双击 `build.cmd`）。

- 菜单入口：`1 启动抓取` / `2 分析（终端）` / `3 网页版分析` / `4 退出`（`q/quit/exit` 亦可）。
  抓取与网页服务都是 **Ctrl+C 停 → 回菜单**。
- 抓取链路**没有一行 JavaScript**；唯一的 JS 是菜单 3 的界面（`internal/webreport/static/`，
  零构建 Vue 3 + Tailwind 浏览器版，`//go:embed static` 内嵌 → 无需 npm/联网，产物仍是一个 exe）。
  套路见用户级 skill `vue-tailwind-nobuild-webapp`。
- 配置只有 `config.json`（exe 同目录），**不接受命令行参数**（用法是双击）。相对路径（outDir/rawDir）
  相对 **exe 所在目录**展开（双击时 cwd 不可靠）。`webPort`（默认 8765）被占时自动换端口并**在终端说明**。
- 文档分工：`README.md` 只放介绍与上手；「为什么」进 `docs/design.md`，测试/排查进 `docs/testing.md`。
- `legacy/`（旧 Node 实现）2026-10-16 已整体删除，可取回：`git checkout ac9e35c -- legacy/`。
- **`data/` 是入库的**（3269 个文件，实际抓到的 CSV 就在 git 里）；`raw/` 不入库。

## 版本号（唯一来源 = 根目录 `VERSION`，当前 0.1.0）

- 升版本**只改 `VERSION`，然后必须重跑 `build.cmd`**。产物名 / exe 文件属性 / 程序内显示三处全由它推导。
  用户口径：对外从 0.1.0 起（旧的 1.4.0 是内部迭代号，不继承）。
- `build.cmd` 三步：`set /p VER=<VERSION` → `go-winres make --in winres\winres.json --out rsrc
  --arch amd64 --file-version=%VER% --product-version=%VER%` → `go build -ldflags "-X main.version=%VER%"
  -o "tv-capture-v%VER%.exe" .`（**就地**，无 dist）。
- `winres/winres.json` **不许写版本号**（会被命令行覆盖）；该 JSON **不支持注释**（写 `_comment` 会 unmarshal 报错）。
- `version_test.go`（根包）钉五处：VERSION 一行三段数字 / `main.go` 兜底字面量一致 / build.cmd 关键字齐全
  （含 `-o "tv-capture-v%VER%.exe"`）且纯 ASCII + CRLF / build.cmd 命令里不许出现 `dist`/`copy`/`mkdir` /
  winres.json 无版本键。`go test .` 会拦漂移。
- **产物就地放根目录**（2026-10-16 22:30 用户拍板，此前试过 `dist\` 又退回）：outDir/rawDir/config.json
  全按 exe 所在目录解析，「搬目录 = 换一份数据」。`/rsrc_windows_*.syso` 已 gitignore。
- `go-winres` 装在 `C:\Users\jiang\go\bin`。

## 核心约定（改代码必须守）

### 目录与分层（2026-10-16 重构后定型）
- **根 `main.go` 只是薄入口**：只有 `main()`（`os.Exit(cli.Run(version))`）和 `version` 变量。
  菜单、输入、三个菜单项的编排全在 **`internal/cli`**（cli/menu/prompt/capture/analyze/web 六个文件按菜单项切）。
- **入口不搬进 `cmd/`**（明确否决）：单二进制 + 双击即用，`go run .`/`go build .` 要能用；搬走会连带
  改 build.cmd 包路径、`.syso` 输出目录（**`.syso` 只被链接进与它同目录的包**，搬走 main 包而 syso 留根目录
  → exe 属性页变空且无任何报错）、version_test.go 里全部按根目录解析的相对路径。
  `version` 必须留在 `main` 包：`-X` 只能设包级变量，build.cmd 写死 `-X main.version`（version_test.go 拦）。
- **`internal/` 保持扁平**，不按层套子目录（导入路径就是包路径，插一层只是让 import 变长）。
  层次由「谁能 import 谁」表达：入口 main → 编排(cli, capture) → 协议(cdp, wsclient, tvproto)
  / 领域(ingest, store, config, rawlog) → **表现层(termreport, webreport)**；logx 谁都能用。
- **表现层是一对孪生包**：`internal/termreport`（菜单 2 终端表格）与 `internal/webreport`（菜单 3 JSON+页面），
  入参都是 `store.Scan` 的 ScanResult，都只读、都不重算统计量。唯一允许写两份的是**显示格式**
  （千分位 / `1时5分` / KB-MB），由 `internal/webreport/testdata/format.test.cjs` 对着
  `internal/termreport/report_test.go` 的期望值断言。
- 理由与「重构前为什么不行」写在 `docs/design.md` 的「代码怎么分工」一节。

### 抓取语义
- **只被动监听**：不拖图表、不刷新、不注入脚本、无自动回拉（风控优先，刻意）。
- **只收 5 分钟**（`intervalSeconds: 300`），其他周期整段跳过并记日志。
- 周期靠**相邻时间戳的众数差**反推（帧里无周期字段），反推出来前 K 线**暂存不落盘**；
  反推**按（会话，序列）分别做**（`seriesKey{scope,name}`）——多个图表会话的序列都叫 `sds_1`，混着投票会污染。
- **标的归属只认 `symbol_resolved`**，按「会话+序列」判：`config.symbol` >（`create_series` 的
  序列→品种键绑定 + `symbol_resolved` 的品种键→pro_name）> 该会话唯一品种 > 页面唯一品种 >
  URL `?symbol=` 猜测（日志说明是猜的）> **暂存不落盘**。不知道写哪个目录时绝不猜。
- **页面 WS 上不只有图表数据**：侧边栏/自选/报价的 `qsd` 同样带 `pro_name` 且是别的品种
  （踩过：MNQ1! 的 K 线写进了 `NASDAQ_NDX/`）。旧的「任意消息里搜 pro_name」是 bug，已删。
  `tvproto` 只暴露 `SymbolResolved`/`SeriesBinding`/`MessageScope`/`QuoteNames`（后者仅诊断）。
- **CSV 写入 = 整体重写**：内存按 `(标的,日期)` 聚合 `t→bar`，每 `flushSeconds` 用 tmp+rename 重写整个日文件
  （重复数据结构上不可能，故无「是否已存在」判断）。**启动时必须先把当日文件读回内存**，否则重启接着拖会覆盖。
- 时间列是**本地时区明文 `yyyy-MM-dd HH:MM`（开盘时间）**，文件名 `yyyy-MM-dd.csv`。换机器/时区会影响
  「哪一天」的划分。被 Excel 占用会写失败，保持「脏」下周期重试。

### 菜单 / 交互
- 全程只有**一个** `bufio.Reader(os.Stdin)`（`menu.go` 的 `readLine`）。菜单用缓冲 reader 而 `waitEnter`
  直接读 `os.Stdin` 会绕开缓冲，表现成「按一次回车跳过两项」。`waitEnter` 在 stdin 非终端时直接返回；
  stdin EOF → 菜单直接退出，不空转。

### 只读性与网页版硬约束
- **「分析」必须只读**：走 `store.Scan`，**不要**用 `store.Store`（它 Open 就建目录、往 `_symbols.json` 分配目录名）。
  测试用「扫描前后目录快照一致」钉住。**网页版（菜单 3）走同一个 `store.Scan`**，只是把结果换成 JSON。
- ① **统计量只在 Go 里算**，`app.js` 只做排版。唯一例外是**显示格式**（千分位 / `1时5分` / KB-MB），
  靠 `internal/webreport/testdata/format.test.cjs` 逐条对着 `report_test.go` 断言（`go test ./internal/webreport/`
  会调 node 跑，没 node 则 skip）。
- ② 服务只绑 `127.0.0.1`，**没有任何能改数据/触发动作的接口**（只有 `/api/analysis` + 内嵌静态）。
- ③ 改 `internal/webreport/static/` 下任何文件都要**重新 `go build`**（`//go:embed` 是编译期快照）。
- ④ **日历视图**：一个月份状态**全局共用**（`month`），翻一次所有标的与合计一起翻。格子里的数字**没有一个**
  是前端算的 —— `store.DateTotal` 的 `Issues`（这天有几个标的有坏行/重复/读不了）由 `webreport` 透传成
  `"issues"`，**不许**改成让 JS 扫 `folders` 去凑（那就是第二份口径）。高亮异常日是**变淡不隐藏**（空格子
  表示「没数据」）。格子观感全在 `style.css` 的 `.cal-*`，**一个 Tailwind 工具类都不用**：承载格子的模板是
  运行期字符串，放在 `index.html` 的 `<template id="tv-cal-tpl">`（**必须在 `#app` 之外**），`app.js` 的
  `templateText()` 取 `innerHTML` 注册成 `cal-grid` 组件（空了就克隆 `.content` 兜底）。Tailwind 浏览器版
  扫 DOM 推导样式，运行期才出现的类名有漏扫风险，漏了就是格子挤成一坨且**无任何报错**。日期算术在前端
  （`monthOf/isMonth/addMonth/todayMonth/monthCells/weekday/monthsOf`），断言用**固定日期**
  （`2026-10-01` 周四 → 前补 3 格、整月 35 格），**不要写依赖「今天」的期望值**。点表头排序已删；`sortFiles` 留着。
- ⑤ **判定口径**：**「缺口」「最大断档」已从网页版删掉**（天天出现，看不出东西）。
  `store.Gap{StartT,EndT,Bars}` = 缺失那几根的**应当时刻**，`EndT = StartT + (Bars-1)*step`（不从后一根倒推，
  脏数据下会算出 EndT < StartT）。实测 2451 份日文件：1604 份 1 处、4 份 2 处、843 份没有。`store` 里的
  `Missing/Span/MaxGap` **还在**（终端菜单 2 仍用），只是不进 JSON。
  **「异常日」= 根数不在该星期几应有取值里，或文件有坏行/重复/读不了**。取值表只在 `store.ExpectedBars`：
  周一 `{216,204}`、周二~周五 `{276}`、周六 `{72,60}`、周日 `{0}`。**周一和周六必须各两个值**（美国夏令时让
  本地时刻挪 1 小时）。实测 817 天**零误报**，被判不符的 36 天全是美国节假日。表经 `meta.expectRule` 送到页面
  印出来，**HTML 里不许再抄一份数字**。**「数量不一致」= 同一天有数据的标的根数不全相同**（`store.markMismatch`），
  **只比当天有数据的标的**（没这份文件不能当 0 根，否则新标的入库前的每天都集体亮黄）。实测 25/817 天（MGC 提前收市）。
- **统计口径与落盘共用一份代码**：`store.readDayFile`/`parseRow`/`readIndexFile`。各写一份迟早漂移。

### 测试
- **禁止无头浏览器**（用户明确要求：慢、中断不可见）。替代 = 假 CDP 服务端，`go test ./...` 全离线可跑；
  人工联调用户自己开浏览器。
- **不开浏览器也能查出「页面全白」的便宜办法（已用通）**：node 里用 `vue.global.prod.js`（完整版）
  `Vue.compile(tpl,{onError:e=>{throw e},onWarn:w=>warns.push(w.message)})`，模板语法错/指令写错当场报出。
  两个坑：① 需要极小 `document` 替身（`createElement` 返回带 `innerHTML` getter/setter、`textContent`、
  `children[0].getAttribute` 的对象，编译器只用它解 HTML 实体）；② 必须 `globalThis.Vue = Vue`。
  再配正则把模板里引用的标识符（先剥字符串字面量与 `v-for` 别名）回查 `app.js`。脚本一次性，不进仓库。

## 本机环境：SAC、签名

> **现状（2026-10-16 22:20 实测）**：`HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy` 的
> `VerifiedAndReputablePolicyState = 0`（即「关闭」），`go test ./internal/capture/` 正常通过。
> 下面是被拦时的记录，**当备用排查线索看，别当现在的常态**。

- 智能应用控制（SAC）曾拦 `go test ./internal/capture/` 与 `go run .`，报
  `An Application Control policy has blocked this file`。触发条件只剩一条：**由 `go.exe` 亲自启动的那个二进制**
  （同一份 exe 换个启动者就能跑；路径/目录/时间窗口/内容全已排除；`go vet` 与其他包的测试不受影响）。
  SAC **没有单应用白名单** → 换目录、安全中心排除项、自签名全无效。出路：① `go test -c -o <临时目录>` +
  `cd <包目录>` 手动启动；② 切「评估」（会被系统自动升回强制）；③ 关 SAC（代价见下）。
- **本机「关闭 SAC」单向不可逆，不要赌**：确认框原文是「…无法重新打开或切换到评估模式，除非转到 Windows
  恢复选项并重置此电脑」（用户截图）。微软 FAQ 有相反说法，**与本机提示冲突 → 以本机提示为准**。
  教训：不能把「查到的官方说法」当成「在这台机器上成立」，动手前让用户点开确认框看一眼。
- **给 exe 签名绕开 SAC**：只认微软云信誉或**受信任提供商**签发的证书，**自签名无效**（网上「自签名 +
  装受信任根即可」的教程与官方口径冲突，不要采信）。要签就买（推荐 Microsoft Trusted Signing，用 **RSA**）。
  **`go test`/`go run` 场景签名在原理上赶不上**（go 自己编译并立即执行，中间没有签名时机）—— 签名只对
  **固定产物、且发给别人**时有意义。「开开发人员模式可豁免 SAC」**不成立**。

## 关键事实（实测，别忘）

- Chrome ≥ 136：给默认用户数据目录加 `--remote-debugging-port` 被**静默忽略**，只能走
  `chrome://inspect/#remote-debugging` 的开关（≥144）；每个浏览器会话重勾，每次外部连接弹一次 Allow，**不点不通**。
- 新版 Chrome 只开放**浏览器级 WS**，`/json/version`、`/json/list` 一律 404。端口与 WS 路径从
  `<User Data>/DevToolsActivePort` 读（第一行端口，第二行 `/devtools/browser/<uuid>`）。
  页面发现走 `Target.getTargets` + `Target.attachToTarget`(flatten)。
- **一个标签页同时只能有一个调试会话** → 该页开着 DevTools 时 attach 必失败（代码 1 分钟退避）。
- 抓包里**有** `symbol_resolved`：`{"m":"symbol_resolved","p":["cs_X","sds_sym_1",{…"pro_name":"CME_MINI:MNQ1!"…}]}`；
  配套绑定信令 `{"m":"create_series","p":["cs_X","sds_1","s1","sds_sym_1","5",300,""]}` 是**客户端发上去的**
  （所以上行帧也要扫，但只扫这一条）。数据消息的 `p[0]` 是会话 id。`modify_series` 未在抓包中出现过
  （按同形状试解析，属**推断**）。
- 报价里的伪品种键形如 `={"adjustment":"splits",…,"symbol":"CME_MINI_DL:MNQ1!"}`，不是品种名；
  `CME_MINI:MNQ1!` 的报价里还夹着 `"underlying-symbol":"NASDAQ:NDX"`。
- 按形状抽 K 线**不会**误抽报价/指标：655 帧真实抓包里 211 个含 K 线的段全部是图表会话的
  `du`/`timescale_update`，`qsd`（含 57KB 那条）与 `st` 一根都没贡献。
- 帧：`~m~<长度>~m~<负载>`；心跳两种形态都见过 —— 裸 `~h~1` 与 `~m~4~m~~h~1`。声明长度与实测不符约 3%
  （5305 段中 146 段）→ 对不上时必须退化为「切到下一个帧头之前」；**但「负载长度必须等于声明长度」不能省**，
  否则声明超长时会把后面几段一起吞掉。
- K 线形状 `{s:[{i,v:[t,o,h,l,c,v]}]}`，t 是 Unix 秒，series 名是它上一层的键名（`sds_1`…）。`st1`/`st2` 是指标
  （`st` 键）、`qsd` 是报价，都不会被误抽。
- `lbs.bar_close_time` **不是**收盘时间，是图表右侧可视边界。单帧最大实测 247KB（WS 客户端必须支持 64 位长度
  与分片；写帧必须掩码）。图表**按需**取数：可视范围走出缓存窗口才发 `request_more_data` → 拖图表就是填历史的手段。
- 期货休市（CME，UTC）：每日 21:00–22:00 结算，周六全天 + 周日 22:00 前。正常形态：工作日日文件 276 根，
  其中固定一段断档**本地 05:00 → 05:55（缺 12 根）**就是那 1 小时结算；周六约 60/72 根（凌晨收市）。
- **CSV 时间列是明文本地时间**，所以「时间戳是否落在文件名日期之外」这类自检**恒为 0**（判据自己是循环的），
  别写这种永远不响的检查（曾写过一版「越界根数」，恒 0 后删掉）。有效报警是**重复行计数**：写入是整体重写 +
  原子改名，同一时间戳不可能重复，不为 0 就是文件被手工改过或被别的工具合并过。
- **`build.cmd` 必须同时是纯 ASCII 和 CRLF**（两条都承重）：
  · **ASCII**：cmd.exe 回读批处理按**字节偏移**定位行，多字节字符会让偏移与解码字符错位 → **一行尾段被单独当
    命令执行**，报 `'。' is not recognized as an internal or external command`，相邻 `echo` 整行消失
    （**输出的中文本身还是对的**，极易误判成显示问题）。实测：旧脚本 237 个非 ASCII 字节一直正常；加版本号
    机制后涨到 1071，跑一半就炸。→ **这个文件里的日志一律写英文，中文留给程序自己打**。
  · **CRLF**：`core.autocrlf=true` 只在 checkout/commit 转换，**工具直接写盘不会被转** → 用写文件方式生成
    .cmd 会留 LF，cmd.exe 假设每个换行 2 字节、逐行累积错位，每行行首被吃掉（实测把 `exit /b 1` 吃成 `it`）。
  · 两条由 `version_test.go` 的 `TestBuildCmdIsAsciiAndCRLF` 断言。**教训：「只看第一行」的检查会骗人**
    （那次第一行恰好是 CRLF）。整文件核：
    `python -c "d=open('build.cmd','rb').read(); print(sum(1 for b in d if b>0x7f), d.count(b'\n')-d.count(b'\r\n'))"` 两个数都得 0。

## 用户偏好

- 需要「先纠正认知误区，再给可执行结论」，并结合他自己工程的具体文件举例；喜欢可视化图示配合讲解。
- 重视结论要有实测支撑，讨厌凭空推断 —— 汇报时如实标出哪些验证过、哪些还是猜测。
- 对**不可逆操作**敏感：声称「清除数据」时选择归档而非物理删除并明确告知，他没有反对。
