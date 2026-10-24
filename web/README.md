# 看盘 · 复盘（前端）

本仓库的**前端应用**，与根目录的 Go 抓取工具同仓共处。

当前状态：**v1.1（对应程序 0.3.0）已实现** —— K 线回放、标注、模拟交易、多人观看、
浅色/深色两套配色、价格轴按标的 tick 走、实例存档（关掉程序再进来还在）。
界面是「能用、看得清」的程度，没有做视觉设计 —— 每一版只保证功能对、口径对。

## 它和根目录那个 Go 程序是什么关系

| | 根目录 Go 程序 | 本目录 |
| --- | --- | --- |
| 干什么 | 通过 CDP 旁路抓 TradingView 的 5 分钟 K 线，写成 CSV；菜单 2/3 出只读报表 | 菜单 4 的复盘界面：把 CSV 逐根回放、画标注、做模拟交易 |
| 形态 | 单文件 exe，双击即用，零第三方依赖 | Vite 构建的 SPA，产物为 `dist/` |
| 前端 | 菜单 3 的网页版分析页，零构建的 Vue + Tailwind（`//go:embed` 进 exe） | 常规构建流程（Vite + TS），**运行期**从磁盘读取，不进 exe |

依赖方向是**单向**的：Go 不认识这个目录的源码，只在自己那一侧按 `replayDistDir`
（默认 `"web/dist"`）去读构建产物。所以 Go 不能跨目录 `//go:embed`，也就意味着
**改完前端必须 `npm run build`**，而发布时要连 `dist/` 一起带走。

开发期有两个接触点，都在 `vite.config.ts` 里：

- `/ws` → `127.0.0.1:8790`（菜单 4 的 WebSocket，`ws: true`）
- `/api` → `127.0.0.1:8765`（菜单 3 的只读接口，目前前端没用它）

两个端口被占时 Go 那边都会自动换端口并在终端说明 —— 换了就要同步改这里。

## 技术栈

| 依赖 | 版本 | 说明 |
| --- | --- | --- |
| Vue | 3.5 | `<script setup>` + 组合式 API |
| Pinia | 4 | 一个 store：`stores/replay.ts`（连接、实例、标注、挂单、事件流全在里面） |
| Vue Router | 5 | 一条路由 `/` → `ReplayView.vue` |
| TypeScript | 6.0 | `vue-tsc` 做类型检查 |
| Tailwind CSS | 4.3 | CSS-first 配置，**没有 `tailwind.config.js`** |
| Vite | 8 | 构建与开发服务器 |
| ESLint 10 + Prettier 3 | | flat config，格式交给 Prettier |

**画图不用图表库**：Canvas 2D 画 K 线 / EMA20 / 挂单线 / 十字光标，
SVG 画标注并承接命中测试（hover、点击、拖动）。两层共用同一套坐标变换（`chart/scale.ts`）。

**配色分两处**：页面（顶栏、面板、输入框）吃 `style.css` 里的语义色 CSS 变量，
`data-theme` 一换全变；画布（K 线、网格、轴）是 Canvas 像素，不跟 CSS 变量走，
所以 `chart/render.ts` 里有两套 `ChartTheme`，换主题要靠重画。两处都必须改到，
否则症状是「页面变白了、K 线还是黑的」。

## 常用命令

```bash
npm install

npm run dev         # 开发服务器 → http://127.0.0.1:5173（/ws 转发到 8790）
npm run type-check  # vue-tsc 类型检查
npm run lint        # ESLint（--max-warnings 0）
npm run lint:fix
npm run format      # Prettier 写回
npm run build       # 类型检查 + 生产构建 → dist/（菜单 4 托管的就是它）
npm run preview     # 预览 dist/
```

完整的启动方式（两种）见 [../docs/复盘设计.md](../docs/复盘设计.md) 的「怎么跑起来」。

## 目录结构

```
web/
├─ index.html              入口 HTML（页面标题在这里）
├─ vite.config.ts          @ 别名、dev server 绑 127.0.0.1、/ws 与 /api 代理
├─ eslint.config.js        flat config
├─ .prettierrc.json        Prettier 规则
├─ src/
│  ├─ main.ts              装配：pinia → router → mount
│  ├─ App.vue              只放 <RouterView />
│  ├─ style.css            Tailwind 入口 + 两套配色的语义色 + 满高 + 滚动条
│  ├─ types/protocol.ts    与 Go 侧 internal/replay/protocol.go 一一对应的消息类型
│  ├─ net/socket.ts        ReplaySocket：连接 / 自动重连 / 按 type 分发
│  ├─ stores/index.ts      pinia 实例
│  ├─ stores/replay.ts     唯一的业务 store（连接状态、实例、事件日志、操作动作）
│  ├─ router/index.ts      一条路由：/ → ReplayView
│  ├─ views/ReplayView.vue 页面骨架（页头 / 画布 / 底部 Tab 条）
│  └─ components/
│     ├─ chart/scale.ts    坐标变换（时间索引 ⇄ 像素、价格 ⇄ 像素）+ 刻度 + tick 吸附 + 轴上时间文案
│     ├─ chart/snap.ts     Ctrl 吸附 OHLC（`nearestOHLC` / `snapAtCursor`，与后端同口径）
│     ├─ chart/trades.ts   成交流水 → 「开仓均价 → 平仓价」线段（纯函数，只读）
│     ├─ chart/render.ts   Canvas 绘制（K 线、EMA20、挂单/成本线、成交三角、十字光标）+ 两套 ChartTheme
│     ├─ ChartPanel.vue    Canvas + SVG 容器与全部指针交互（平移 / 缩放 / 画 / 拖 / 吸附）
│     ├─ ChartToolbar.vue  工具选择、颜色、EMA20 开关、步进、接管、机器人
│     ├─ InstancesMenu.vue 右上角折叠面板：标的、随机/指定时间建实例、实例列表与删除
│     ├─ TradeDock.vue     底部停靠区：模拟交易 / 操作记录 / 交易记录 三个 Tab，可整体收起
│     ├─ RecordingPanel.vue 交易记录列表（盈亏 / MFE·MAE / 进出场）+ 回放播放器
│     ├─ TradeFloat.vue    下单浮窗：fixed、拖标题栏换位置、折叠成 44px 图标、位置存本机
│  │     ├─ OrderPanel.vue    三列横排：下单 / 持仓与挂单 / 成交流水
│     ├─ TradePanel.vue    成交流水
│     └─ EventLog.vue      后端广播的事件流（谁做了什么）
└─ dist/                   构建产物（已 gitignore）
```

## 数据流（改代码前先理解这条链）

```
菜单 4（Go）──http/ws─→ 浏览器
                         socket.ts 收到消息 → store.handleMessage
                                              ├ 状态类（welcome / symbols / instances / instance_state）→ 直接覆盖
                                              └ 事件类（event）→ 按 payload 更新实例，同时 push 进事件日志
                         Vue 模板与 Canvas/SVG 只读 store
用户操作 ──send()──→ 发给 Go → Go 改状态 → 广播回来 → 才更新界面
```

**除了缩放和平移，没有任何一个操作是本地直接改状态的**（这是需求里的硬性约束）：

| 操作 | 本地立即做 | 等后端确认 |
|---|---|---|
| 缩放 / 平移 / 十字光标 | ✅ 全部本地 | 不发消息 |
| 右键菜单算价格 / 复制到剪贴板 | ✅ 全部本地 | 不发消息（挂单那两项才发） |
| 步进、下单、撤单、切工具选择后的落笔 | ❌ | ✅ |
| 射线（**单击即完成**，预览跟着鼠标走） | ✅ 预览跟着鼠标走 | ✅ 单击后由后端编号并广播定稿 |
| 其余四种标注（第 1 次单击之后、第 2 次之前） | ✅ 预览跟着指针走 | ✅ 再单击后由后端编号并广播定稿 |
| 定稿后复位工具（交回【指针】） | ✅ 纯界面状态，不发消息 | — |
| 拖动已有标注 | ✅ 跟随指针 | ✅ 松手后发 `annotation_move` |
| 删除标注 | ❌ | ✅ |

预览和后端定稿的差别只有一处：**Ctrl 吸附**。前端为了手感自己算一版最近的 OHLC，
后端按同一规则再算一遍（开盘价优先、严格更近才替换）—— 两边都必须一致，
不一致的表现是"松手后图形跳一下"。

前端这套取舍**只有一份实现**：`chart/snap.ts` 的 `nearestOHLC` / `snapAtCursor`。
十字光标那一帧、以及左上角那句提示都调 `snapAtCursor`（按住 Ctrl 就能在单击**之前**
看见落点），定稿端点和拖动端点调 `nearestOHLC`。**改它时同时看一眼 Go 的
`internal/replay/instance.go` 里的 `nearestOHLC`** —— 两份必须逐字同口径。

光标下那根 K 线由 `scale.barAtCursor(x)` 给出（`ref` = 存坐标用的「锚点 + 向右几根」，
`bar` = 吸附用的那根）。**别在别处再算一遍「光标在哪根 K 线上」**：十字光标的竖线走的是
`nearestIndex`，两条路不一致的表现是「吸住时竖线跳到旁边那根上」。

## 坐标模型（最容易被写坏的地方）

标注的坐标只有两种东西：**K 线时间字符串**和**价格**。没有像素、没有索引。

- 横轴是「已揭示 K 线的索引」，`index = bars.findIndex(b => b.time === time)`，
  时间字符串就是 CSV 里那一列的原文（唯一标识，不转换、不带时区）；
- 索引到像素：`x = plot.left + (index - scroll) * barWidth + barWidth / 2`；
- 价格到像素：`y = priceTop + (priceMax - price) / (priceMax - priceMin) * priceHeight`；
- 反变换在 `chart/scale.ts`，`indexAtX` / `priceAtY`，**不要在任何组件里另写一份**。

拿索引当存储是错的：窗口一平移、`cursor` 一变，同一个索引就指向另一根 K 线了。

## 约定（改代码前先看这几条）

**Tailwind v4 是 CSS-first。** 没有 `tailwind.config.js`，别去建，建了不会被读取。
要定义主题色、字号、自定义变体时，在 `src/style.css` 里加 `@theme` / `@custom-variant` 块。

**涨跌色在 `render.ts` 的 `DARK_THEME` / `LIGHT_THEME` 里，改配色只改那里**，别在组件里散落颜色。
当前口径（v1.1 明确要求，**注意方向：绿涨红跌**）：

| | 阳线 | 阴线 |
|---|---|---|
| 深色 | 绿实心 `#22c55e` | 红实心 `#ef4444` |
| 浅色 | 白填充 + 黑框 `#111827` | 全黑 |

浅色用「空心 / 实心」而不是红绿，是因为白底上纯红纯绿刺眼、红绿色弱看着一样。
要整体翻成「红涨绿跌」（中国习惯），把两张表的 up/down 换一下、并且**两套都换**——
只换深色会出现「切一下主题涨跌就反了」，那比配色不好看严重得多。

**`@` 是 `src/` 的别名**，改的话两处必须同步：`vite.config.ts` 的 `resolve.alias` 和
`tsconfig.app.json` 的 `paths`。只改一处会「构建能过、编辑器报红」或者反过来。

**dev server 显式绑 `127.0.0.1:5173`。** 这个 `host` 不能删：本机 `localhost` 解析到 IPv6 `::1`，
不显式绑 IPv4 的话，从 `127.0.0.1` 访问会连不上。

**不要在前端算钱。** 成交、持仓、（已实现/未实现）盈亏全部来自后端广播。
`grep` 一下会发现前端没有任何一处"按价格乘出来"的盈亏 —— 这是刻意的：
撮合规则只有一份，在 `../internal/replay/instance.go`。

**改 `types/protocol.ts` 时同步改 Go 的 `protocol.go`。** 两边字段名是逐字对应的
（`instanceId`、`annotationId`、`orderId`、`placedIndex`…），没有缩写。
字段名对不上不会报错，只会表现成"某个面板一直是空的"。

## 主题、tick 与布局（v1.1）

**主题**：`stores/replay.ts` 里的 `theme`（`'light' | 'dark'`）→ 写进
`document.documentElement.dataset.theme` + 存 `localStorage['tv-replay-theme']`。
`style.css` 用 `:root` / `:root[data-theme='light']` 两套变量，组件只写语义类
（`bg-panel` / `text-ink` / `border-line` / `text-dim` / `text-faint` / `bg-accent`…）。
`@custom-variant dark` 已经把 `dark:` 接到 `data-theme` 上 —— 个别必须分主题的细节可以写
`dark:`，它跟的是**用户选的主题**，不是系统偏好。
`index.html` 里有一小段内联脚本，在样式生效前先把 `data-theme` 挂上（否则上次选浅色会先闪一下深色）。
内联脚本里的键名必须和 `THEME_KEY` 一致。

**tick**：`store.tick` 来自 `instance.tick`（Go 侧从 `data/_symbols.json` 读，缺失时走
`store.DefaultTick` 的兜底表）。它决定三件事：
① 价格轴刻度步长抬到它的整数倍（`niceTicks`，不会出现这个标的没有的价）；
② **十字光标与右键菜单的价格吸附到它的整数倍**（`snapToTick`，MES 只能是 .00/.25/.50/.75）；
③ 显示小数位（`decimalsForTick`，最少 2 位 —— 价格已经四舍五入到两位，只留一位会显示错）。
下单框的 `step` 也用它。

**画布上的交易痕迹**（v1.2）：`render.ts` 里画三样东西，**都画在 Canvas 上，不是标注** ——
所以它们不能选中、不能拖动、不能删除：

| 画什么 | 数据来源 | 何时出现 |
|---|---|---|
| 成本线（贯穿全宽 + 左边标签「多 N @ 价 · 已实现 P」） | `instance.position` / `realizedPnl` | `position.side !== 'flat'` |
| 成交三角（买上 / 卖下，尖顶住成交价） | `instance.trades` | 有成交就有 |
| 开平连线（虚线，开仓均价 → 平仓价） | `chart/trades.ts` 的 `closedTrades()` | 有平仓段就有 |

配对规则跟后端 `applyFill` 的**平均成本法**一致（加仓摊平、反向平多少、反手成本 = 本笔价）；
`pnl` 直接用 `Trade.realizedPnl`，前端不算钱。

**右键菜单**（`ChartPanel.vue`）：`@contextmenu` 在按下那一刻把 `priceAtY` 吸附到 tick 并存进
`menu`，三项动作都基于这个定死的价格。「N 手」读的是 `store.orderQuantity` —— 和底部下单框
是同一个数。菜单自身 `@pointerdown.stop`，否则画布的 pointerdown 会先把它关掉。

**布局**：`ReplayView.vue` = 顶栏（状态 / 当前实例 / 主题 / 标的与实例 / 离开）+ 工具条 +
画布 + `TradeDock`（底部 Tab）。左右两条常驻栏都去掉了，目的是把空间让给画布；
实例面板点开才占地方，底部 Tab 条可整体收起。

## 还没做的事

- 视觉设计、响应式（窄屏只是能挤着用，没有专门适配）
- 指标只有 EMA20（BarCount 副图已移除，后续版本再考虑别的），没有成交量面板
- 标注只有线段 / 矩形 / 文字，没有箭头、斐波那契、测量工具
- 存档是整体快照：实例特别多、K 线特别长时会变慢（见 `../internal/replay/persist.go`）
- 前端没有自动化测试：靠 `type-check` / `lint` / `build` 三道 + 手工两标签页演练
