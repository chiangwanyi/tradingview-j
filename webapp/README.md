# webapp

PF（自营盘）账户模板管理的前端。左边菜单、右边内容，右侧那块空间以后要接「账户列表」。

```
npm install
npm run dev     # http://127.0.0.1:5174
npm run check   # 模板自洽校验的自检（6 项）
npm run build   # 类型检查 + 生成 dist/
```

## 数据在哪

**在浏览器 localStorage 里**，key 是 `webapp.pf-templates.v1`。后端目前只有
`PropFirmTemplateService`，**没有 REST 接口**（`src/main/java/.../controller/` 里只有
`VersionController`）。接真接口时只改 `src/data/store.ts` 的 `load/persist`，组件不用动。

首次打开会种 6 条 LucidFlex 样例（25K/50K/100K 各一条 EVAL + 一条 FUNDED，串成两条链）。
想回到初始状态：浏览器控制台 `localStorage.removeItem('webapp.pf-templates.v1')` 后刷新。

## 目录

```
src/
  App.vue                    左右两栏；导航切换 + 「新建模板」把编辑框叫起来
  views/TemplatesView.vue    标题 / 阶段筛选 / 卡片横向翻页 / 弹窗挂载点
  views/PlaceholderView.vue  还没做的导航项
  components/AppSidebar.vue  左侧菜单
  components/PlanCard.vue    一张模板卡
  components/TemplateDialog.vue 建 / 改（表单里全是字符串，提交时才转成数字）
  data/rules.ts              类型 + validate + 金额/比例格式化 —— **校验口径的唯一实现**
  data/store.ts              localStorage 读写、种数据、删模板时顺手断链
scripts/rules-check.mts      validate 的自检，node --experimental-strip-types 直接跑
```

## 和参考图的差异

参考图是深色，这里换成浅色（`src/style.css`，绿色仍是品牌色）。参考图上有、这里没有的：

- **价格区**（ONE TIME PAYMENT / $45.50 / Reset Fee / 优惠）：`PropFirmRules` 里没有价格字段，
  不编数字，那个位置改放「当前阶段 → 达标后」的链信息。
- **Daily Loss Limit**：同样不在规则里，没有编造。
- 顶部的 Pro / Flex / Daily / Direct 段切换，这里当**阶段筛选**（全部 / EVAL / FUNDED / PAYOUT），
  因为模板唯一能分类的维度就是阶段。

## 规矩（和 `PropFirmTemplate.java` 对齐，别在这里另立一套）

- 一个模板只描述**一个阶段**；一套考核是几条模板串成的链（`nextTemplateId`）。
- `stage` 建出来之后不能改（表单里禁用）—— 改它等于把两个阶段缝在一起，已有账户的升级路径会静默错位。
- EVAL 必须有盈利目标；FUNDED 不能有目标。`null` 是**有意义的取值**，不是「没填」。
