# tradingview-j

> 旁路抓你**本人已登录的 Chrome** 里 TradingView 图表页的 WS 行情帧，解 **5 分钟 K 线**，
> 按「一个标的文件夹、一天一个 CSV」落盘。纯 Go、**零第三方依赖**，产物是根目录单文件
> `tv-capture-v<版本>.exe`（当前 `0.31.0`）。只**被动**监听：不注入脚本、不操作图表、无回拉。

## 跑起来

**Chrome 开远程调试**（每个浏览器会话一次）：在已登录 TradingView 的 Chrome 里访问
`chrome://inspect/#remote-debugging` → 勾 **Allow remote debugging for this browser instance** →
打开**图表页**（URL 带 `/chart/`）、周期切 **5 分钟**。

> Chrome 136 起给默认用户目录加 `--remote-debugging-port` 会被**静默忽略**，只能走这个开关：
> 代价是每次外部连进来弹一次 Allow。

另需 **Go 1.26+** 和 `go install github.com/tc-hib/go-winres@latest`（构建期工具）。

```bat
build.cmd               :: 前端 + exe 一次做完，任一阶段失败就地中断
build.cmd --no-web      :: 只编 exe，沿用上次的前端产物
```

三阶段固定顺序：`cd web && npm run build`（→ `web\dist\`，**不进 exe**）→ `go-winres make
--file-version=<v> --product-version=<v>`（→ `.syso`）→ `go build -ldflags "-X main.version=<v>"
-o tv-capture-v<v>.exe .`（→ 根目录 exe）。

```
1 启动抓取（录 CSV + 开分析页）    2 复盘（回放/标注/模拟交易/交易记录）    3 退出
```

版本号只有 `VERSION` 一个来源，落到三处（文件名 / exe 属性 / 程序内横幅与网页页脚），分别由上面的
`-o`、`go-winres`、`-ldflags` 写。**升版本 = 改 VERSION 再重跑 `build.cmd`**；`.syso` 是编译期快照，
别手改别提交。`outDir`/`rawDir` 相对 **exe 所在目录** —— 换目录放 exe 等于换一份数据。

## 菜单 1：抓取 + 分析页

选 1 后：抓取开始等调试端口和图表页（没开就一直等，每 5 秒重试），分析页在 `127.0.0.1:8765` 起来并
替你打开。**你要做的只有一件事**：在 Chrome 里往回拖图表，或用回放模式跳日期 —— 图表走到没缓存的
范围时页面自己会要数据，程序录下来，分析页根数自己涨。`Ctrl+C` 先刷盘、再关分析页，回到菜单。开多个图表标签页会**全部挂上**、各自独立录。

分析页对数据目录做**只读**扫描（不建目录、不写索引），只绑 `127.0.0.1`、**没有能改数据的接口**：
每个标的一张月历，格子里是**当天 K 线根数**（标红 = 与星期应有根数不符或坏行，标黄 = 各标的根数不一致，
灰 = 没数据）；点某天出明细（根数、应有根数、首尾根、**每段断档的起止**）；异常日**变淡不隐藏**。

**实时是三层接力，没有推数据的通道**：`store` 每 `flushSeconds`(1s) 重写脏日文件 → `webreport` 每次
请求先比**磁盘指纹**（名字 + 大小 + 修改时刻），一样发缓存、不一样重扫 → 页面每 2s 拉 `/api/analysis`。
数字永远是磁盘那一刻的样子，只是看得勤。`Scanner` 记住每份文件上次的摘要与指纹，只重解析变过的
（44MB/3300 份：全量 1.2s → 无变化 6ms）。

## 菜单 2：复盘

本地网页逐根回放 `data/` 的 K 线，画标注、做模拟交易。和菜单 1 的区别：那个是**只读报表**，这里是
**有状态的应用**（实例/标注/挂单/成交写 `state/`），但**仍只读数据目录**，一个 CSV 都不改。

- **实例**：右上「标的与实例」→ 选标的（时间留空随机）→ 新建，铺出锚点前 400 根、之后逐根揭示。
  另两入口：【最新】锚在 `data/` 最新一根；【开盘】**随机挑一交易日**、锚在第 1 根（首尾两天不参选）。
  判据是**停盘缺口**不是「0 点」：只能按「和前一根隔 65 分钟」认（`replay/session.go`）。【下一根】
  揭示一根并用其高低撮合挂单（键盘 **→** 同效，长按不连发）；【下一天开盘】跳到下一交易日第 1 根，
  那一夜**逐根撮合**过去（跳过等于抹掉夜里成交）。
- **标注**五种：趋势/射线/矩形/箭头/斐波那契。**【射线】单击完成**，其余两次单击；Shift 画水平线、
  Ctrl 吸附 OHLC；坐标严格是「K 线时间 + 价格」。点一下就能改（拖端点、拖线身、`Del` 删），选中后
  左上出现同类模板条（只换外观不动端点，浮在画布上不占工具条高度）。左上【K 线数据栏】显示该根
  **开高低收 · EMA20 · ATR · 涨跌**（涨跌是**价差**，正绿负红，首根 `—`）。
- **下单**：任意价格右键，单型按价格相对现价的方向自动定；挂单**两对**（手填 / nATR 倒推，算不出时
  置灰）。可**复制价格 / 复制时间**（原样照抄 `time` 字段）。挂单线可拖改价、右端「×」撤单。
  **有仓位时对手单旁显示该挂单成交后的盈亏**（多头 2 手挂卖 3 手 → 按**仓位**只算 2 手；同方向是加仓，
  不显示），口径 `(平 − 开) × 手数 × 每点价值`。
- **模拟交易**：限价/止损/市价，**没有止损止盈类型**（要止损就挂 `stop` 单），无初始资金、不计手续费/
  点差/滑点。浮窗有「市价退仓并取消所有挂单」（先撤光、再按现价平掉整仓）。
- **nATR 定手数**：填 **n**（止损 = n×ATR）和**最大亏损**，给出「每手亏多少 → 下几手」。例：ATR 19.24、
  n=10 → 192.4 点；MNQ 每点 $2 → 每手 $384.80；上限 $2000 → 5.197 → **5 手**（实亏 $1924）。
  四舍五入到最接近上限。按下时**止损自动挂**。
- **指标**（EMA20 / ATR / Doji X + ZigZag）在【指标 ▾】。ATR 是价格区下方**独立副图**，
  **Length 14 + RMA**（对齐 `ta.tr(true)`）；副图无价格坐标，右键下单和画图在那儿不生效。
  **Bar Count**：每根最低价下方标「当天第几根」，每张 CSV 从 #1 重数、只标单数。
- **多人**：第二个标签页是**观众**，能收不能操作；「交还/接管」切换，操作者关页自动释放。底边两个 Tab
  （模拟交易 / 操作记录）；回放中多一枚红色「回放中 x/y」。
- **回放延伸**：走到最后一步可【＋ 加载下一根】，把记录末尾之后的**真实 K 线**演出来。它是**写进记录
  的一步**（`kind=bar_extended`），能跳回能重放，也能在上面录音（每根 9 条）。**只能在末尾加载**。
- **交易记录**独立页，首页**月历按「记录日」排**（今天补做去年 1 月的行情也算今天），按盈亏染色
  （绿赚红亏 + ▼▲），写「N 笔 / ±X$」。点进**当天详情**：KPI + 六张图 + 逐笔明细，
  **点任一行回画布逐格演这一笔**。**只排版，钱仍来自后端。**

改过前端必须重新 `npm run build`；开发期 `cd web && npm run dev`（`/ws` 转 8790）。

## 配置 · 数据 · 结构

程序**不接受命令行参数**，全在 exe 同目录 `config.json`（不存在自动生成），相对路径相对 **exe 目录**：

常改的：`matchUrl`（挂哪个标签页）、`symbol`（留空自动识别）、`intervalSeconds`(300，**只收这个周期**)、
`outDir`/`rawDir`(`data`/`raw`)、`webPort`/`replayPort`(8765/8790)、`flushSeconds`(1)。其余见自动生成
的 `config.json`（带注释）。

```
data/_symbols.json              标的 → 文件夹 / tick / tickValue / commission
data/CME_MINI_MNQ1!/2026-10-15.csv
state/replay-instances.json + instances/ins-1/{meta,bars,annotations,orders,trades,…}.json
presets/replay-templates.json   绘图模板（全局）·  recordings/recordings.json + rec-<ts>/…
raw/<标的>/<时刻>.ndjson
```

CSV 是 `time,open,high,low,close,volume`，**时间列是明文 `yyyy-MM-dd HH:MM`、本机时区、这根的开盘时间**。
写盘整体重写（tmp+rename），启动先读回当日文件再合并。`state/` `recordings/` `raw/` 不入库。

```
VERSION / build.cmd / winres/winres.json / main.go（只有 main() 与 version）
internal/cli       横幅、菜单循环、编排（唯一的 stdin reader）
internal/capture   常驻会话：等端口、等页面、挂监听
internal/cdp       端点发现、命令与事件、挑目标页
internal/wsclient  自写 WS 客户端（大帧 + 分片）
internal/tvproto   分帧、抽 K 线、标的解析
internal/ingest    解码：周期反推、暂存、落盘
internal/store     CSV 存储 + scan.go 只读扫描（并发 + 增量）
internal/config · rawlog · logx · wstest   配置 / 原始帧 / 日志 / 测试假服务端
internal/webreport 分析页：JSON + embed 的 static/（改了要重新 go build）
internal/replay    菜单 2：WS + 实例状态机 + 标注 + 撮合 + 机器人 + 交易记录
internal/strategy  纯函数策略：出「方向 + 止损 + 目标」
web/               复盘前端（Vue 3.5 + Pinia + TS + Tailwind 4 + Vite）
```

分层按「**谁能 import 谁**」：入口 `main` → 编排 `cli`/`capture` → 协议 `cdp`/`wsclient`/`tvproto`
与领域 `ingest`/`store`/`config`/`rawlog` → 表现 `webreport`/`replay`；`logx` 谁都能用。
**入口不搬 `cmd/`**（双击运行，且 `.syso` 只链同目录包）；**`internal/` 保持扁平**。前端 K 线
**不用图表库**（Canvas 画 K 线、SVG 画标注，共用 `chart/scale.ts`），只 `/records` 六图用 ECharts。

> 仓库根还有**独立的 Spring Boot 骨架**（`src/` / `pom.xml`，端口 8791，见 [`SERVER.md`](SERVER.md)），
> 与 Go 程序互不引用。本文只讲 Go 这侧。

## 不能让步的约定

**标的名只认 `symbol_resolved`**，别在整帧里搜 `pro_name` —— 同一条 WS 上还有自选/报价的 `qsd`，
带着**别的品种**。顺序：`config.symbol` → 序列绑定 → 会话内唯一 → 页面内唯一 → URL 猜；给不出结论时
K 线**先攒着不落盘**。**周期靠众数差反推**，按「会话 + 序列」**分别投票**（混投互相污染）；
`lbs.bar_close_time` 是陷阱（图表右侧可视边界，不是收盘时间）。

**统计口径只有一份**：根数、断档、应有根数、异常判定全在 `store.Scan`；在 JS 里重算会出现两份口径，
而**同一目录给出不同根数时用户没法裁决**。**「分析」不许写任何东西**：只走 `store.Scan`
（`store.Store` 一 Open 就建目录）；复盘读 CSV 只用 `ReadDayRows`/`Catalog`，唯一写入口 `persist.go`。
CSV 行格式只在 `store.readDayFile` 定义一次。

**前端预览、后端定稿**：除缩放/平移/十字光标，没有操作在本地改状态；正式状态**永远来自广播**。
唯一两边各算一遍的是 Ctrl 吸附（`chart/snap.ts` 与 `instance.go:nearestOHLC` 同口径，不一致的症状是
「松手后图形跳一下」）。**指标按「是不是盘面事实」分**：EMA20/ATR14 在 Go 里算好下发
（`Bar.ema20`/`Bar.atr14`），Doji X/ZigZag 参数留前端；ATR 周期**钉在 14**，副图是布局的一部分
（`scale.setAtrPane`）。**标注只有「时间 + 价格」**：
像素换窗口就错；端点可带 `offset` 往右越过最后一根，所以延伸段和 K 线严格对齐。

**撮合规则只有一份**：`instance.go`，机器人走同一套方法（策略拿不到 `Instance`，造不出成交价）。
成交价通常是挂单价、跳空取开盘价；本根挂的单从**下一根**才参与撮合。**前端不算钱**，唯一例外是
nATR 定手数（只算手数不产生盈亏，`lib/natr.ts`，「每点价值」必须按 `tickValue / tick`，与后端
`pointValueOf` 漂了用户就会发现「说好亏 $1924，结果不是」）——**止损价不在例外里**，前端只传距离、
**一键退仓是一条消息**：`flatAll` 先平仓再撤单、发 `flat_all`。

**交易记录挂在唯一的广播出口上**（不散在 `do*Locked` 里，否则漏一个动作就表现成「回放少一步」）；
录的是**广播原文**，回放喂给同一个 reducer。**回放延伸不复用 `bar_advanced`**（那条 payload 带持仓
全量快照，延伸段没有成交，照它更新会把仓位改写成「还开着」）；落盘用 `UpdateRecording` **不是
`Save`**（`Save` 会按本次传入的字节清理 `audio-*.wav`，等于删光语音）。

**两条行为契约**：同组保护单（OCO）一根里只成交一张，**两张都被扎到时判止损**；**日历日最后一根
（23:55）无条件平仓 + 撤光挂单**，不留过夜。**机器人 = 策略 + 执行器 + 撮合**：策略出「方向 + 止损价 +
目标价」（**绝对价格不是距离**，换算散两边迟早有一边写反），实现 `OnBar` 即可。

## 测试 · 改代码前

`go test ./...`（全部离线，不需要浏览器）· `cd web && npm run type-check && npm run lint && npm run build`

**前端代码写完后应使用浏览器实际打开页面进行校验**：确认渲染效果、交互行为和运行时报错符合预期，
并据此判断是否通过（不通过就继续改，而不是只看编译和静态检查过了就算完）。
抓取侧的离线测试仍用**假 CDP 服务端扮演 Chrome**（`internal/wstest`），把**真实抓包**
（`testdata/`）当帧事件推下去：握手、分帧、抽 K 线、周期判定、品种识别、落盘、留档全跑一遍。
Chrome 的真实行为建议人工确认一遍：勾远程调试 → 开图表页 → 菜单 1 → 点 Allow → 往回拖几次 →
看 CSV 是否长大、分析页根数是否跟着涨。

- **版本号只改 `VERSION`** 且必须重新构建；**`build.cmd` 必须纯 ASCII + CRLF**（有测试拦）。
- **菜单只能有一个 stdin reader**，否则缓冲里的字符被绕过，表现成「按一次回车跳过两项」。
- **网页版不许有算数字的代码**：改显示格式或日期算术要同步 `format.test.cjs` 的期望值（唯一事实
  来源）。**改了 `webreport/static/` 要重新 `go build`**（`//go:embed`）。
- **「实时」只有一条链路**：写盘 → 指纹变 → 重扫 → 轮询，别加事件推送或让页面直读抓取进程内存。**缓存
  指纹 = 名字 + 大小 + 修改时刻**，别只留大小，也**别算目录自己的 mtime**。
- **`web/dist` 不是 embed 的**（运行期从磁盘读，发布要连 `dist` 一起带走）；**URL 路径用 `path`、磁盘用
  `filepath`**（`filepath.Clean` 会把 `/assets/x.js` 变成 `\assets\x.js`）。
- **存档是快照不是日志**：可加「属于实例」的字段（`omitempty` 让老档照读），别加「属于进程」的
  （谁在操作、日文件索引、机器人协程）。落盘靠 `markDirtyLocked()`（挂在广播出口上）。
- **前端**：`types/protocol.ts` 与 `replay/protocol.go` 逐字对应（对不上不报错，只表现成「面板一直
  空的」）；`@` 别名在 `vite.config.ts` 与 `tsconfig.app.json` 两处同步；**方向色只有一个语义**
  （绿=涨/多/盈，红=跌/空/亏），只在 `--c-up`/`--c-down` 与 `CHART_THEME` 两处。**K 线例外**：蜡烛
  保持黑白（`up`/`down` 都是 `#111827`，只差空心实心）。

抓取属于对 TradingView 服务条款的灰色操作：低频小批量自用可控，**不要高频批量拉、不要全品种刷**，
也别长时间挂着连录几十小时。仓库未声明开源许可。
