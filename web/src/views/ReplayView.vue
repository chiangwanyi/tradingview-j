<script setup lang="ts">
import { computed, onMounted } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import EventLog from '@/components/EventLog.vue'
import InstancePanel from '@/components/InstancePanel.vue'
import OrderPanel from '@/components/OrderPanel.vue'
import TradePanel from '@/components/TradePanel.vue'
import { useReplayStore } from '@/stores/replay'

/**
 * 复盘主界面：左边标的与实例，中间画布，右边交易，底部操作记录。
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

onMounted(() => {
  store.connect()
})
</script>

<template>
  <div class="flex h-screen flex-col bg-slate-950 text-slate-100">
    <header class="flex shrink-0 items-center gap-3 border-b border-slate-800 px-3 py-2">
      <span class="text-sm font-semibold tracking-wide">看盘 · 复盘</span>
      <span class="text-[10px] text-slate-600">v{{ store.me?.version ?? '' }}</span>

      <span class="flex items-center gap-1.5 text-[11px] text-slate-400">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusColor" />
        {{ statusText }}
      </span>

      <span v-if="store.me" class="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-300">
        {{ store.me.name }}
        <span v-if="store.isOperator" class="text-emerald-400">（操作者）</span>
        <span v-else-if="store.instance" class="text-slate-500">（观众）</span>
      </span>

      <div v-if="store.instance" class="flex min-w-0 items-center gap-2 text-[11px] text-slate-400">
        <span class="h-4 w-px bg-slate-700" />
        <span class="truncate font-medium text-slate-200">{{ store.instance.symbol }}</span>
        <span class="font-mono text-[10px] text-slate-500">{{ store.instance.anchor }}</span>
        <span class="text-[10px] text-slate-500">{{ cursorText }}</span>
        <span v-if="store.instance.bot" class="rounded bg-sky-600/20 px-1.5 text-[10px] text-sky-300">
          机器人演示中
        </span>
      </div>
      <div v-else class="text-[11px] text-slate-600">没有进入实例</div>

      <div class="ml-auto flex items-center gap-2">
        <button
          v-if="store.instance"
          type="button"
          class="rounded border border-slate-700 px-2 py-1 text-[11px] text-slate-300 hover:border-slate-500"
          @click="store.leaveInstance()"
        >
          离开
        </button>
      </div>
    </header>

    <div class="flex min-h-0 flex-1">
      <aside class="w-64 shrink-0 overflow-hidden border-r border-slate-800">
        <InstancePanel />
      </aside>

      <main class="flex min-w-0 flex-1 flex-col">
        <ChartToolbar />
        <div class="min-h-0 flex-1 bg-slate-950">
          <ChartPanel />
        </div>
      </main>

      <aside class="flex w-72 shrink-0 flex-col border-l border-slate-800">
        <div class="min-h-0 flex-1 overflow-hidden"><OrderPanel /></div>
        <div class="h-56 shrink-0 overflow-hidden border-t border-slate-800"><TradePanel /></div>
      </aside>
    </div>

    <footer class="h-40 shrink-0 border-t border-slate-800">
      <EventLog />
    </footer>

    <div
      v-if="store.lastError"
      :key="store.errorKey"
      class="fixed right-3 bottom-3 z-50 max-w-md rounded border border-red-700 bg-red-950/95 px-3 py-2 text-xs text-red-100 shadow-lg"
    >
      <div class="flex items-start gap-3">
        <span class="flex-1">{{ store.lastError }}</span>
        <button type="button" class="text-red-300 hover:text-white" @click="store.clearError()">关闭</button>
      </div>
    </div>
  </div>
</template>
