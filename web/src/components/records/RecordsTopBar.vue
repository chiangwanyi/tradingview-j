<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { RouteLocationRaw } from 'vue-router'

import { useReplayStore } from '@/stores/replay'

/**
 * 交易记录页的顶栏（日历页 / 当日详情页共用）。
 *
 * 为什么抽出来：两页的顶栏除了标题和返回目标之外一模一样 —— 版本号、连接状态、
 * 记录目录、刷新。各写一份的下场是「刷新按钮在一页上能用、另一页忘了接」，
 * 而连接状态那种东西一旦不一致，用户就会以为服务挂了。
 *
 * 插槽分两层：默认插槽是**同一行里的控件**（来源切换），`sub` 是第二行
 * （日历的月份导航、详情页的日期导航）。
 */
defineProps<{
  /** 页面标题，例如「交易记录」/「2026年10月24日」 */
  title: string
  /** 左上角返回链接的目标（复盘画布 / 交易日历） */
  backTo: RouteLocationRaw
  /** 返回链接的文字 */
  backText: string
}>()

const store = useReplayStore()

const statusText = computed(() => {
  switch (store.status) {
    case 'open':
      return '已连接'
    case 'connecting':
      return '连接中…'
    default:
      return '已断开（正在重连）'
  }
})

const statusColor = computed(() => {
  switch (store.status) {
    case 'open':
      return 'bg-emerald-500'
    case 'connecting':
      return 'bg-amber-400'
    default:
      return 'bg-red-500'
  }
})
</script>

<template>
  <header class="sticky top-0 z-30 border-b border-line bg-panel/95 backdrop-blur">
    <div class="mx-auto flex w-full max-w-[1440px] flex-wrap items-center gap-2 px-4 py-2">
      <RouterLink
        :to="backTo"
        class="shrink-0 rounded border border-line px-2 py-1 text-[13px] text-dim transition hover:border-accent hover:text-accent"
      >
        ← {{ backText }}
      </RouterLink>
      <h1 class="shrink-0 text-[15px] font-semibold tracking-wide text-ink">{{ title }}</h1>
      <span class="tnum text-[12px] text-faint">v{{ store.me?.version ?? '' }}</span>
      <span class="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-[12px] text-dim">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusColor" />
        {{ statusText }}
      </span>

      <slot />

      <div class="ml-auto flex min-w-0 shrink-0 items-center gap-2">
        <span
          class="hidden max-w-[20rem] truncate text-[12px] text-faint xl:inline"
          :title="store.recordingDir"
        >
          记录目录 {{ store.recordingDir || '（未配置）' }}
        </span>
        <button
          type="button"
          class="rounded border border-line px-2 py-1 text-[13px] text-dim transition hover:border-accent hover:text-accent"
          title="重新扫描记录目录"
          @click="store.listRecordings()"
        >
          刷新
        </button>
      </div>
    </div>
    <slot name="sub" />
  </header>
</template>
