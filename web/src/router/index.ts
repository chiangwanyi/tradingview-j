import { createRouter, createWebHistory } from 'vue-router'

import type { RouteRecordRaw } from 'vue-router'

/**
 * 两条路由：复盘画布（`/`）与交易记录（`/records`）。
 *
 * 为什么交易记录要从画布里拆出去（v0.19.0）：底部那 180px 的抽屉只够扫一眼摘要，
 * 而「我最近赚没赚、靠什么赚、哪类单子在亏」是要占满一屏对着图看的。
 * 拆开之后两边都干净了 —— 画布只管画布，统计页只管统计。
 *
 * 剩下的东西仍然留在一条路由里（实例、标注、交易、语音）：它们是**同一块盘面**上的
 * 不同侧面，服务端也是按实例存的，用路由拆开只会把「当前实例」这种状态切碎。
 *
 * 两页之间传递的只有一件事：**要回放哪一条记录**（`stores/replay.ts` 的
 * `pendingPlaybackId`）。所以不是把记录塞进 URL，也就不需要 `/records/:id` 这种路由。
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'replay',
    component: () => import('@/views/ReplayView.vue'),
  },
  {
    path: '/records',
    name: 'records',
    component: () => import('@/views/RecordsView.vue'),
  },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})
