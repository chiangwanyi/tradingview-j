# 看盘 · 复盘（前端）

本仓库的**前端应用**，与根目录的 Go 抓取工具同仓共处。

当前状态：**只有工程骨架 + 一个 HelloWorld 验证页，页面设计尚未开始。**

## 它和根目录那个 Go 程序是什么关系

| | 根目录 Go 程序 | 本目录 |
| --- | --- | --- |
| 干什么 | 通过 CDP 旁路抓 TradingView 的 5 分钟 K 线，写成 CSV | 面向「看盘 + 复盘」的界面 |
| 形态 | 单文件 exe，双击即用，零第三方依赖 | Vite 构建的 SPA |
| 前端 | 菜单 3 的网页版分析页，零构建的 Vue + Tailwind（内嵌在 exe 里） | 常规构建流程（Vite + TS），独立开发与部署 |

两者**互不依赖**：Go 那边不会 import 本目录，本目录也还没调用 Go 那边的接口。

开发期唯一的一处接触点：`vite.config.ts` 里把 `/api` 代理到抓取工具的网页版服务
（`127.0.0.1:8765`）。那个端口被占时 Go 程序会自动换端口并在终端说明 —— 换了就要同步改这里。

## 技术栈

| 依赖 | 版本 | 说明 |
| --- | --- | --- |
| Vue | 3.5 | `<script setup>` + 组合式 API |
| Pinia | 4 | 状态管理（已装好并注册，但**目前没有任何 store**） |
| Vue Router | 5 | 路由，视图按路由懒加载 |
| TypeScript | 6.0 | `vue-tsc` 做类型检查 |
| Tailwind CSS | 4.3 | CSS-first 配置，**没有 `tailwind.config.js`** |
| Vite | 8 | 构建与开发服务器 |
| ESLint 10 + Prettier 3 | | flat config，格式交给 Prettier |

## 常用命令

```bash
npm install

npm run dev         # 开发服务器 → http://127.0.0.1:5173
npm run type-check  # vue-tsc 类型检查
npm run lint        # ESLint（--max-warnings 0）
npm run lint:fix
npm run format      # Prettier 写回
npm run build       # 类型检查 + 生产构建 → dist/
npm run preview     # 预览 dist/
```

## 目录结构

```
web/
├─ index.html              入口 HTML（改页面标题来这里）
├─ vite.config.ts          插件、@ 别名、dev server、/api 代理
├─ eslint.config.js        flat config
├─ .prettierrc.json        Prettier 规则
├─ src/
│  ├─ main.ts              装配：pinia → router → mount
│  ├─ App.vue              只放 <RouterView />
│  ├─ style.css            Tailwind 入口（目前只有一行 @import）
│  ├─ router/index.ts      一条路由：/ → HomeView
│  ├─ stores/index.ts      pinia 实例（还没有 store 文件）
│  └─ views/
│     └─ HomeView.vue      HelloWorld，仅用于验证项目能跑起来
└─ dist/                   构建产物（已 gitignore）
```

## 当前阶段：空画布

除了上面列出的文件，**没有任何界面代码** —— 没有布局外壳（侧栏 / 顶栏）、没有公共组件、
没有业务 store、没有自定义主题色或暗色模式。之前那批占位页面（看盘 / 复盘 / 标的 / 设置 / 404）
和它们配套的 `layouts/`、`components/`、`stores/ui.ts` 已全部删除。

这么做的目的是：**让页面设计从零开始，不被已有结构暗示带偏。**

唯一还带界面信息的遗留物是 `index.html` 里的 `<title>看盘 · 复盘</title>`。

## 约定（改代码前先看这几条）

**Tailwind v4 是 CSS-first。** 没有 `tailwind.config.js`，别去建，建了不会被读取。
要定义主题色、字号、自定义变体时，在 `src/style.css` 里加 `@theme` / `@custom-variant` 块。
**目前这个文件是空的，只有一行 `@import 'tailwindcss';`，这是刻意的。**

**`@` 是 `src/` 的别名**，改的话两处必须同步：`vite.config.ts` 的 `resolve.alias` 和
`tsconfig.app.json` 的 `paths`。只改一处会「构建能过、编辑器报红」或者反过来。

**dev server 显式绑 `127.0.0.1:5173`。** 这个 `host` 不能删：本机 `localhost` 解析到 IPv6 `::1`，
不显式绑 IPv4 的话，从 `127.0.0.1` 访问会连不上。

**`/api` 代理到抓取工具的 `8765`。** 那边端口被占时会自动换端口并在终端说明 ——
换了要同步改这里。（目前还没有代码用它。）

## 还没做的事

- **所有页面设计**：布局、组件、主题色、暗色模式
- 任何行情数据接入（图表库、WS、轮询）
- 任何接口调用与请求封装（`/api` 代理已配好，但还没有代码用它）
- 状态持久化
- 单元测试 / 端到端测试
- 国际化
