import { createRouter, createWebHistory } from 'vue-router'

import type { RouteRecordRaw } from 'vue-router'

/**
 * 两条路由，各占一个「页面」，互不嵌套：
 *
 *   `/`            账户总览（左菜单 + 右边内容，见 `AccountsShellView.vue`）
 *   `/trade/:id`   交易页：一个账户 + 一块 K 线画布
 *
 * 交易页**故意不做成总览里的一层**：从账户卡片点【继续交易】时用 `window.open`
 * 开一个新标签页，于是同一个账户可以并排开着看、各自记住缩放和手里的工具，
 * 关掉一个也不影响另一个。要让「新标签页」成立，它就必须是可刷新、可直接粘地址的 URL，
 * 所以这里是路由，不是组件里的一个 ref。
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'accounts',
    component: () => import('@/views/AccountsShellView.vue'),
  },
  {
    path: '/trade/:id',
    name: 'trade',
    component: () => import('@/views/TradeView.vue'),
    props: true,
  },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})
