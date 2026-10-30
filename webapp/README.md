# webapp

PF（自营盘）的前端：**账户总览**（首页）、**账户模板**、以及**交易页**（K 线画布）。
左边菜单、右边内容；交易页单独占一个路由，从账户卡片点【继续交易】开成新标签页。

```
npm install
npm run dev     # http://127.0.0.1:5174
npm run check   # 模板自洽校验的自检（6 项）
npm run build   # 类型检查 + 生成 dist/
```

两条路由（`src/router/index.ts`）：

| 路径 | 页面 | 说明 |
|---|---|---|
| `/` | `AccountsShellView` | 左菜单 + 账户总览 / 模板页 |
| `/trade/:id` | `TradeView` | 一个账户 + 一块 K 线画布 |

## 数据在哪

**在后端**，前端不自己算账：

- `/api/pf-templates`（`PropFirmTemplateController`，表 `prop_firm_template`）—— 模板列表缓存在
  `src/data/store.ts` 的 `store.list`。
- `/api/pf-accounts`（`PropFirmAccountController`，表 `prop_firm_account`）—— 账户列表在
  `views/AccountsView.vue` 里，卡片自己再按需拉 `/stages`。
- `/api/bars`（`BarController`，**只读**）—— 交易页的 K 线：`/api/bars/symbols` 取标的清单，
  `/api/bars?code=&day=` 取某一天的 5 分钟 K 线。数据来自 `data/<folder>/<yyyy-MM-dd>.csv`，不进库。

任何写操作之后重新拉一遍 —— 服务端是唯一事实来源。余额、阶段、规则快照都由后端给，
前端**不**用成交自己算一遍（两处算法必然分叉）。

开发前先把后端跑起来（`./mvnw spring-boot:run`，127.0.0.1:8791）；
`vite.config.ts` 里配了 `/api` → 8791 的 proxy，所以前端只写相对路径 `/api/...`。

库是空的也不会自己长样例：页面会显示「这个阶段还没有模板」，从左边栏的「+」新建。
后端返回里 null 字段会被 `non_null` 整个省掉（读到 `undefined`），所以判断一律用 `== null`。

## 目录

```
src/
  App.vue                    只有 <RouterView />（页面在路由里）
  router/index.ts            / = 账户总览，/trade/:id = 交易页
  views/AccountsShellView.vue 左右两栏；导航切换 + 「+」按当前页分流（建账户 / 建模板）
  views/AccountsView.vue     账户总览：筛选（全部/进行中/已失败）+ 卡片网格 + 新建弹窗
  views/TradeView.vue        交易页：账户概况 + 标的/日期选择 + 画布 + 底部持仓面板
  views/TemplatesView.vue    标题 / 阶段筛选 / 卡片横向翻页 / 弹窗挂载点
  views/PlaceholderView.vue  还没做的导航项
  components/AppSidebar.vue  左侧菜单
  components/AccountCard.vue 一张考核卡（余额/目标/回撤/进度条/升级/改名/流水）
  components/AccountDialog.vue 开账户：选模板 + 起名
  components/PlanCard.vue    一张模板卡
  components/TemplateDialog.vue 建 / 改（表单里全是字符串，提交时才转成数字）
  components/ChartPanel.vue  画布：Canvas（K 线/指标/挂单/光标）+ SVG（图形）—— 从 web/ 搬来
  components/ChartToolbar.vue 画布上方：绘图工具 / 模板条 / 周期 / 指标
  components/TradeFloat.vue  可拖动的下单浮窗（下单未接通，点了只给提示）
  components/TradeDock.vue   底部：持仓 / 挂单 / 成交流水
  components/chart/*.ts      纯函数渲染层（render/scale/viewport/annotations/atr/…）—— 原样搬来
  stores/trade.ts            交易页状态（web/ 的 replay store 的本地版，见下）
  data/rules.ts              类型 + validate + 金额/比例格式化 —— **校验口径的唯一实现**
  data/accounts.ts           调 /api/pf-accounts：增删改查、升级/判失败、阶段流水、进度算给人看
  data/bars.ts               调 /api/bars：标的清单 + 某一天的 K 线
  data/store.ts              调 /api/pf-templates：列表缓存、建/改/删、删后断链
  data/api.ts                统一响应体拆包 + JSON 请求体 + 错误文案（两份 store 共用）
scripts/rules-check.mts      validate 的自检，node --experimental-strip-types 直接跑
```

## 交易页：从 `web/` 搬来了什么，没搬什么

画布本来在 `web/`（复盘前端），那边连的是 Go 复盘服务的 WebSocket。这里搬的是**渲染与交互**，
数据换成 `GET /api/bars`，状态换成 `stores/trade.ts`（`web/src/stores/replay.ts` 的本地版，
字段名保持一致，所以 `ChartPanel.vue` 等组件只改了 import）。

| 搬来了 | 没搬（以及为什么） |
|---|---|
| `components/chart/*.ts` 全部渲染层（K 线、缩放平移、指标、吸附、ATR、Bar Count） | `stores/playback.ts` + 回放面板：回放属于复盘页 |
| 画布交互（画线五种工具、模板、拖动、右键下单菜单） | 语音讲解（`VoiceFloat` / `VoiceTape` / 录音）：属于复盘 |
| 工具栏（周期切换、指标设置）、下单浮窗、持仓/挂单/成交面板 | 操作记录（`EventLog`）：数据来自 `audit_event`，接口还没做 |
| | 机器人 / 实例列表 / 接管操作权：多人复盘那套概念，单用户交易页没有对应物 |

**没接通的三个动作**（点了会给一句明确提示，不产生任何假数据）：

- 下单 / 撤单 / 改价 / 一键平仓 —— 撮合还在设计阶段（`TRADING-ENGINE-DESIGN.md`），后端没有交易接口。
- 逐根推进（「下一根」）—— 同上，K 线现在一次给一整天、全部揭示。
- 画线与模板**只存在这个标签页的内存里**，刷新即失。它们不改余额、不改持仓，所以允许本地生效；
  但页面上有一条常驻说明写着这件事，别让人以为画完就存住了。

页面顶部默认落在**账户起点那一天**（`startDayFile`），标的默认第一个有数据的；两者都能在页头改
（账户不绑单一标的，可以同时做 MES / MNQ / MGC）。

## 和参考图的差异

参考图是深色，这里换成浅色（`src/style.css`，绿色仍是品牌色）。参考图上有、这里没有的：

- **价格区**（ONE TIME PAYMENT / $45.50 / Reset Fee / 优惠）：`PropFirmRules` 里没有价格字段，
  不编数字，那个位置改放「当前阶段 → 达标后」的链信息。
- **Daily Loss Limit**：同样不在规则里，没有编造。
- 顶部的 Pro / Flex / Daily / Direct 段切换，这里当**阶段筛选**（全部 / EVAL / FUNDED / PAYOUT），
  因为模板唯一能分类的维度就是阶段。
- 首页账户卡上的 **Trading Days / 一致性百分比**：要按成交逐根重算，本页没有这两项，
  卡片放的是账户行里现成的余额 / 目标 / 回撤线 / 一致性上限和阶段起点，不编数字。
  参考图的 **Reset** 按钮也不做 —— 余额只能由成交重算后同步，没有「重置」这个动作。

## 规矩（和 `PropFirmTemplate.java` 对齐，别在这里另立一套）

- 一个模板只描述**一个阶段**；一套考核是几条模板串成的链（`nextTemplateId`）。
- `stage` 建出来之后不能改（表单里禁用）—— 改它等于把两个阶段缝在一起，已有账户的升级路径会静默错位。
- EVAL 必须有盈利目标；FUNDED 不能有目标。`null` 是**有意义的取值**，不是「没填」。
