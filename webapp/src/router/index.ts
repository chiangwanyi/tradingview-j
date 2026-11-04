import { createRouter, createWebHistory } from 'vue-router'

import type { RouteRecordRaw } from 'vue-router'

/**
 * 路由分两层：**总览外壳（`AccountsShellView`）+ 它的子页面**，以及独立的**交易页**。
 *
 * ## 为什么左菜单也变成路由（2026-11-01）
 *
 * 之前外壳里只有一个 `ref('accounts')` 加一个 `detailId`，左菜单五项、账户详情都是
 * 组件内部状态，URL 永远是 `/`。后果是三条都踩过：
 *
 *   1. 在「操作记录」按 F5，回到账户总览 —— 刷新即丢页；
 *   2. 想把「某个模板页」「某个账户」发给别人，没有地址可发；
 *   3. 浏览器后退键退的是整个应用的历史，不是「上一页」。
 *
 * 现在「看的是哪一页」只有一个来源：URL。`/accounts` `/templates` `/audit` `/records`
 * `/profile` 一一对应左菜单的五项，账户详情是它下面的一层 `/accounts/:id`。
 * 高亮哪一项由当前路由**反推**（见 `AccountsShellView` 的 `active`），不再另存一份状态。
 *
 * ## 为什么详情是路由、但不是独立一级
 *
 * `detail` 在语义上就是「账户页里的一层」（整页替换列表，不弹窗），所以挂在 `/accounts/`
 * 下面：刷新 `/accounts/<id>` 直接进那一个账户的详情。
 *
 * ## 为什么交易页留在外面
 *
 * `/trade/:id` 由卡片上的【继续交易】用 `window.open` 开成**新标签页**：同一账户可以
 * 并排开着看、各自记住缩放和手里的工具，关掉一个也不影响另一个。所以它必须是
 * 一个能独立刷新、能直接粘地址的顶层路由，不能挂在这层外壳里。
 *
 * ## `/` 去哪了
 *
 * 老书签和被写死的 `/` 都还能用：空路径直接 redirect 到 `/accounts`。用 redirect 而不是
 * alias，是为了让地址栏里永远写着一个确定的页面名 —— 这正是本次改动的目的。
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: () => import('@/views/AccountsShellView.vue'),
    children: [
      { path: '', redirect: { name: 'accounts' } },
      {
        path: 'accounts',
        name: 'accounts',
        component: () => import('@/views/AccountsView.vue'),
      },
      {
        // `props: true`：详情页要的就是 `id` 这一个参数，不由它自己去读 `route.params`。
        path: 'accounts/:id',
        name: 'account-detail',
        component: () => import('@/views/AccountDetailView.vue'),
        props: true,
      },
      {
        path: 'templates',
        name: 'templates',
        component: () => import('@/views/TemplatesView.vue'),
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('@/views/AuditLogView.vue'),
      },
      {
        path: 'records',
        name: 'records',
        component: () => import('@/views/TradeRecordsView.vue'),
      },
      {
        // 「我的资料」还没做：标题由路由给，占位页不再自己认一个 key。
        path: 'profile',
        name: 'profile',
        component: () => import('@/views/PlaceholderView.vue'),
        props: { title: '我的资料' },
      },
    ],
  },
  {
    path: '/trade/:id',
    name: 'trade',
    component: () => import('@/views/TradeView.vue'),
    props: true,
  },
  {
    // 认不出来的地址回总览，而不是留一张白页（外壳都没挂上，页面上连提示都没有）。
    path: '/:pathMatch(.*)*',
    redirect: { name: 'accounts' },
  },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})
