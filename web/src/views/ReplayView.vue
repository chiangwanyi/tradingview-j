<script setup lang="ts">
import { computed, onMounted } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import InstancesMenu from '@/components/InstancesMenu.vue'
import TradeDock from '@/components/TradeDock.vue'
import TradeFloat from '@/components/TradeFloat.vue'
import { useReplayStore } from '@/stores/replay'

/**
 * 复盘主界面：一条顶栏 + 画布占满 + 底部 Tab（模拟交易 / 操作记录）+ 浮在页面上的下单窗。
 *
 * 布局是 v1.1 调整过的：左侧那一栏「标的与实例」搬进了右上角的折叠面板，
 * 右侧的交易栏搬到了底部 Tab —— 两处都只为了同一个目标：**把空间让给画布**。
 *
 * v0.8.0 又把「下单」这一列从底部面板里提出来，做成一枚可拖动、可折叠成小图标的浮窗：
 * 步进、看盘、挂单是交替进行的，下单框必须随手能点到，又不能再吃掉画布的高度。
 *
 * 整个应用只有这一页。实例是服务端概念，这里只负责「进入/退出/观看」，
 * 关掉标签页不会删除实例（下次连上还在列表里）。
 */

const store = useReplayStore()

const statusText = computed(() => {
  switch (store.status) {
    case 'open':
      return '已连接'
    case 'connecting':
      return '连接中…'
    default:
      return '已断开（正在自动重连）'
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

const cursorText = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  return `第 ${inst.cursor + 1} 根 / 已加载 ${inst.bars.length} 根`
})

const themeLabel = computed(() => (store.theme === 'dark' ? '浅色' : '深色'))

onMounted(() => {
  store.connect()
})
</script>

<template>
  <div class="flex h-screen flex-col bg-surface text-ink">
    <header class="flex h-11 shrink-0 items-center gap-3 border-b border-line bg-panel px-3">
      <span class="text-[15px] font-semibold tracking-wide">看盘 · 复盘</span>
      <span class="text-[11px] text-faint">v{{ store.me?.version ?? '' }}</span>

      <span class="flex items-center gap-1.5 text-[12px] text-dim">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusColor" />
        {{ statusText }}
      </span>

      <span v-if="store.me" class="rounded bg-elev px-1.5 py-0.5 text-[11px] text-dim">
        {{ store.me.name }}
        <span v-if="store.isOperator" class="text-emerald-600 dark:text-emerald-400">（操作者）</span>
        <span v-else-if="store.instance" class="text-faint">（观众）</span>
      </span>

      <div v-if="store.instance" class="flex min-w-0 items-center gap-2 text-[12px] text-dim">
        <span class="h-4 w-px bg-line" />
        <span class="truncate font-medium text-ink">{{ store.instance.symbol }}</span>
        <span class="tnum text-[11px] text-faint">{{ store.instance.anchor }}</span>
        <span class="text-[11px] text-faint">{{ cursorText }}</span>
        <span class="tnum text-[11px] text-faint">tick {{ store.tick }}</span>
        <span v-if="store.instance.bot" class="rounded bg-sky-600/20 px-1.5 text-[11px] text-sky-600 dark:text-sky-300">
          机器人演示中
        </span>
      </div>
      <div v-else class="text-[12px] text-faint">没有进入实例</div>

      <div class="ml-auto flex items-center gap-2">
        <button
          type="button"
          class="rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
          :title="`切换到${themeLabel}模式`"
          @click="store.toggleTheme()"
        >
          {{ themeLabel }}
        </button>
        <InstancesMenu />
        <button
          v-if="store.instance"
          type="button"
          class="rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
          @click="store.leaveInstance()"
        >
          离开
        </button>
      </div>
    </header>

    <main class="relative flex min-h-0 flex-1 flex-col">
      <ChartToolbar />
      <div class="min-h-0 flex-1">
        <ChartPanel />
      </div>

      <!--
        错误提示贴着画布右下角（不是窗口右下角）：底部现在有交易栏，
        fixed 到 bottom-3 会被它整个盖住，报错了也看不见。
      -->
      <div
        v-if="store.lastError"
        :key="store.errorKey"
        class="absolute right-2 bottom-2 z-50 max-w-md rounded border border-red-300 bg-red-50/95 px-3 py-2 text-[13px] text-red-800 shadow-lg dark:border-red-700 dark:bg-red-950/95 dark:text-red-100"
      >
        <div class="flex items-start gap-3">
          <span class="flex-1">{{ store.lastError }}</span>
          <button type="button" class="text-red-500 hover:opacity-70" @click="store.clearError()">关闭</button>
        </div>
      </div>
    </main>

    <TradeDock />

    <!--
      下单浮窗：`position: fixed`，挂在这里只是因为它属于这一页；
      它自己管位置（拖到哪儿记在哪儿）、自己管折叠，不参与上面的 flex 布局。
    -->
    <TradeFloat />
  </div>
</template>
