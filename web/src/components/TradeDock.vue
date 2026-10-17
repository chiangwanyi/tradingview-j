<script setup lang="ts">
import { computed, ref } from 'vue'

import EventLog from '@/components/EventLog.vue'
import OrderPanel from '@/components/OrderPanel.vue'
import { useReplayStore } from '@/stores/replay'

/**
 * 底部停靠区：模拟交易 / 操作记录 两个 Tab（需求要求「以 Tab 切换显示」）。
 *
 * 两个面板都用 v-show 而不是 v-if —— 切 Tab 时不该丢掉滚动位置，
 * 操作记录也要能继续自动滚到最新一条。
 *
 * 可以整体收起（只留 Tab 条），这样想全力看盘时画布能再高 180px。
 */

const store = useReplayStore()
const tab = ref<'trade' | 'log'>('trade')
const collapsed = ref(false)

const tabClass = (key: 'trade' | 'log') =>
  [
    'rounded-t px-3 py-1 text-[12px] transition',
    tab.value === key && !collapsed.value
      ? 'bg-elev text-ink shadow-[inset_0_1px_0_var(--c-line)]'
      : 'text-dim hover:text-ink',
  ].join(' ')

const summary = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  const p = inst.position
  const pos = p.side === 'flat' ? '空仓' : `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)}`
  return `${pos} · 挂单 ${store.openOrders.length} · 成交 ${inst.trades.length}`
})
</script>

<template>
  <section class="shrink-0 border-t border-line bg-panel">
    <div class="flex h-8 items-center gap-1 px-2">
      <button type="button" :class="tabClass('trade')" @click="((tab = 'trade'), (collapsed = false))">
        模拟交易
      </button>
      <button type="button" :class="tabClass('log')" @click="((tab = 'log'), (collapsed = false))">
        操作记录
        <span class="text-faint">({{ store.log.length }})</span>
      </button>

      <span class="ml-auto truncate text-[11px] text-faint">{{ summary }}</span>
      <button
        type="button"
        class="ml-2 rounded border border-line px-1.5 py-0.5 text-[11px] text-dim hover:border-accent hover:text-accent"
        @click="collapsed = !collapsed"
      >
        {{ collapsed ? '展开面板 ▲' : '收起面板 ▼' }}
      </button>
    </div>

    <div v-show="!collapsed" class="h-[12.75rem] border-t border-line">
      <OrderPanel v-show="tab === 'trade'" />
      <EventLog v-show="tab === 'log'" />
    </div>
  </section>
</template>
