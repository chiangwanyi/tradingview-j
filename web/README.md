# 看盘 · 复盘（前端）

本仓库的**前端应用**，与根目录的 Go 抓取工具同仓共处。

当前状态：**复盘第一版已实现**（K 线回放 + 标注 + 模拟交易 + 多人观看）。
没有画好看的界面 —— 这一版只保证功能对、口径对。

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

**画图不用图表库**：Canvas 2D 画 K 线 / EMA / BarCount / 挂单线 / 十字光标，
SVG 画标注并承接命中测试（hover、点击、拖动）。两层共用同一套坐标变换（`chart/scale.ts`）。

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
│  ├─ style.css            Tailwind 入口 + 满高 + 滚动条
│  ├─ types/protocol.ts    与 Go 侧 internal/replay/protocol.go 一一对应的消息类型
│  ├─ net/socket.ts        ReplaySocket：连接 / 自动重连 / 按 type 分发
│  ├─ stores/index.ts      pinia 实例
│  ├─ stores/replay.ts     唯一的业务 store（连接状态、实例、事件日志、操作动作）
│  ├─ router/index.ts      一条路由：/ → ReplayView
│  ├─ views/ReplayView.vue 页面骨架（页头 / 左栏 / 中间画布 / 右栏 / 底部日志）
│  └─ components/
│     ├─ chart/scale.ts    坐标变换（时间索引 ⇄ 像素、价格 ⇄ 像素）+ 刻度
│     ├─ chart/render.ts   Canvas 绘制（K 线、EMA20、BarCount、挂单线、十字光标）
│     ├─ ChartPanel.vue    Canvas + SVG 容器与全部指针交互（平移 / 缩放 / 画 / 拖 / 吸附）
│     ├─ ChartToolbar.vue  工具选择、步进、机器人、缩放按钮
│     ├─ InstancePanel.vue 标的列表、随机/指定时间建实例、实例切换与删除
│     ├─ OrderPanel.vue    限价/止损下单、挂单列表、撤单、持仓与盈亏
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
| 步进、下单、撤单、切工具选择后的落笔 | ❌ | ✅ |
| 画标注（拖动中） | ✅ 虚线预览 | ✅ 松手后由后端重算并广播定稿 |
| 拖动已有标注 | ✅ 跟随指针 | ✅ 松手后发 `annotation_move` |
| 删除标注 | ❌ | ✅ |

预览和后端定稿的差别只有一处：**Ctrl 吸附**。前端为了手感自己算一版最近的 OHLC，
后端按同一规则再算一遍（开盘价优先、严格更近才替换）—— 两边都必须一致，
不一致的表现是"松手后图形跳一下"。

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

**涨红跌绿。** 中国口径，和欧美相反。当前是 `render.ts` 里 `DARK_THEME` 的两个常量
（`up: '#ef4444'` / `down: '#22c55e'`），改配色只改这里，别在组件里散落硬编码颜色。

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

## 还没做的事

- 界面美化、布局响应式、暗色/亮色切换（现在只有一套深色）
- 指标只到 EMA20 + BarCount（需求就是这么写的），没有成交量面板
- 标注只有线段 / 矩形 / 文字，没有箭头、斐波那契、测量工具
- 复盘状态不持久化：刷新页面会重建连接，标注和挂单在服务端内存里（服务重启即清空）
- 前端没有自动化测试：靠 `type-check` / `lint` / `build` 三道 + 手工两标签页演练
