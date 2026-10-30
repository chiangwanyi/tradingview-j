<script setup lang="ts">
import { computed, ref } from 'vue'

import OrderPanel from '@/components/OrderPanel.vue'
import { useTradeStore } from '@/stores/trade'

/**
 * 底部停靠区：**持仓 / 挂单 / 成交流水**。
 *
 * web/ 里的这一块有三个 Tab（模拟交易 / 操作记录 / 回放），这里只留第一个：
 * 操作记录来自后端的 `audit_event`，回放属于复盘页 —— 两者在这个页面都没有内容可显示，
 * 留一个永远空着的 Tab 只是噪音。
 *
 * 可以整体收起（只留标题条），这样想全力看盘时画布能再高 200px。
 */
const store = useTradeStore()
const collapsed = ref(false)

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
    <div class="flex h-9 items-center gap-2 px-2">
      <span class="whitespace-nowrap rounded-t bg-elev px-3 py-1 text-[13px] text-ink">模拟交易</span>
      <span class="ml-auto hidden truncate text-[12px] text-faint sm:inline">{{ summary }}</span>
      <button
        type="button"
        class="ml-2 shrink-0 rounded border border-line px-2 py-0.5 text-[12px] text-dim hover:border-accent hover:text-accent"
        @click="collapsed = !collapsed"
      >
        {{ collapsed ? '展开面板 ▲' : '收起面板 ▼' }}
      </button>
    </div>

    <div v-show="!collapsed" class="h-[14.5rem] border-t border-line">
      <OrderPanel />
    </div>
  </section>
</template>
