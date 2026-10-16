# tradingview-j 项目长期笔记

## 这个项目是什么（2026-10-16 起是 Go 版）

通过 CDP 接入**用户本人已登录的 Chrome**，**被动**旁路抓 TradingView 图表页的 WebSocket 帧，
解出 5 分钟 K 线，写成 CSV。形态是**一个 Go 程序**：

- 产物 `tv-capture.exe`（根目录；`go build -o tv-capture.exe .` 或双击 `build.cmd`）。
- **入口是菜单式**（v1.1.0 起）：`1 启动抓取` / `2 分析已抓取的数据` / `3 退出`。
  抓取中 Ctrl+C = 刷盘 + 打印统计 + **回到菜单**（不退出程序）。
- **没有任何 JavaScript**：只被动监听，不需要页面注入，也不需要扩展，所以"必须保留的 JS"是零。
- **配置只有 `config.json`**（exe 同目录），**不接受任何命令行参数** —— 使用方式是双击。
  相对路径（outDir/rawDir）相对 **exe 所在目录**展开，因为双击时 cwd 不可靠。
- **零第三方依赖**，`go.mod` 里没有 require；WebSocket 客户端自写在 `internal/wsclient`。
- 结构：`main.go`（横幅+菜单分发）+ `menu.go`（菜单与输入）+ `analyze.go`（选项 2 的排版）
  + `internal/{config,cdp,wsclient,tvproto,ingest,store,rawlog,capture,logx}`
  （`store/scan.go` 是只读扫描）+ `internal/wstest`（测试专用假 WS 服务端）
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
- **统计口径与落盘共用一份代码**：`store.readDayFile` / `parseRow` / `readIndexFile`。
  两边各写一份解析迟早会漂移，表现成"文件里 288 行、分析说 287 根"，用户没法判断信谁。
- **测试禁止无头浏览器**（用户明确要求：慢、且中断不可见）。替代 = 假 CDP 服务端。
  `go test ./...` 全离线可跑，人工联调由用户自己开浏览器做。
  本机跑 capture 包测试可能需要 `GOTMPDIR=$PWD/.tmpbuild`（系统临时目录被
  Application Control 策略拦过：`fork/exec ... blocked this file`）。

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
