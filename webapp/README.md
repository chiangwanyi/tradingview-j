# webapp

PF（自营盘）的前端：**账户总览**（首页）和**账户模板**。左边菜单、右边内容。

```
npm install
npm run dev     # http://127.0.0.1:5174
npm run check   # 模板自洽校验的自检（6 项）
npm run build   # 类型检查 + 生成 dist/
```

## 数据在哪

**在后端**，前端不自己算账：

- `/api/pf-templates`（`PropFirmTemplateController`，表 `prop_firm_template`）—— 模板列表缓存在
  `src/data/store.ts` 的 `store.list`。
- `/api/pf-accounts`（`PropFirmAccountController`，表 `prop_firm_account`）—— 账户列表在
  `views/AccountsView.vue` 里，卡片自己再按需拉 `/stages`。

任何写操作之后重新拉一遍 —— 服务端是唯一事实来源。余额、阶段、规则快照都由后端给，
前端**不**用成交自己算一遍（两处算法必然分叉）。

开发前先把后端跑起来（`./mvnw spring-boot:run`，127.0.0.1:8791）；
`vite.config.ts` 里配了 `/api` → 8791 的 proxy，所以前端只写相对路径 `/api/...`。

库是空的也不会自己长样例：页面会显示「这个阶段还没有模板」，从左边栏的「+」新建。
后端返回里 null 字段会被 `non_null` 整个省掉（读到 `undefined`），所以判断一律用 `== null`。

## 目录

```
src/
  App.vue                    左右两栏；导航切换 + 「+」按当前页分流（建账户 / 建模板）
  views/AccountsView.vue     账户总览：筛选（全部/进行中/已失败）+ 卡片网格 + 新建弹窗
  views/TemplatesView.vue    标题 / 阶段筛选 / 卡片横向翻页 / 弹窗挂载点
  views/PlaceholderView.vue  还没做的导航项
  components/AppSidebar.vue  左侧菜单
  components/AccountCard.vue 一张考核卡（余额/目标/回撤/进度条/升级/改名/流水）
  components/AccountDialog.vue 开账户：选模板 + 起名
  components/PlanCard.vue    一张模板卡
  components/TemplateDialog.vue 建 / 改（表单里全是字符串，提交时才转成数字）
  data/rules.ts              类型 + validate + 金额/比例格式化 —— **校验口径的唯一实现**
  data/accounts.ts           调 /api/pf-accounts：增删改查、升级/判失败、阶段流水、进度算给人看
  data/store.ts              调 /api/pf-templates：列表缓存、建/改/删、删后断链
  data/api.ts                统一响应体拆包 + JSON 请求体 + 错误文案（两份 store 共用）
scripts/rules-check.mts      validate 的自检，node --experimental-strip-types 直接跑
```

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
