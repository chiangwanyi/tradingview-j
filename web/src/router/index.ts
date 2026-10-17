import { createRouter, createWebHistory } from 'vue-router'

import type { RouteRecordRaw } from 'vue-router'

/**
 * 第一版只有复盘这一页。实例、图表、标注、交易都在同一页里切换视图，
 * 用路由拆开反而会把「当前实例」这种服务端状态切碎，所以保持一条路由。
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'replay',
    component: () => import('@/views/ReplayView.vue'),
  },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})
