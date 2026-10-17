<script setup lang="ts">
import { computed } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import { useReplayStore } from '@/stores/replay'

/**
 * 成交流水。每一笔都对应一张被触发的挂单，`realizedPnl` 是这一笔平掉的部分实现的盈亏
 * （开仓/加仓时为 0）—— 这是后端算好的，前端不重算，避免出现两份口径。
 */

const store = useReplayStore()

const trades = computed(() => (store.instance?.trades ?? []).slice().reverse())
const totalRealized = computed(() => store.instance?.realizedPnl ?? 0)
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="flex items-center justify-between border-b border-slate-800 px-3 py-2">
      <span class="text-xs font-semibold tracking-wide text-slate-300">成交记录</span>
      <span class="text-[10px] text-slate-500">{{ trades.length }} 笔</span>
    </div>

    <div v-if="trades.length === 0" class="p-3 text-[11px] text-slate-500">
      还没有成交。挂一张单，然后点「下一根」让它有机会被触发。
    </div>

    <div v-else class="min-h-0 flex-1 overflow-auto">
      <table class="w-full text-[10px]">
        <thead class="sticky top-0 bg-slate-900 text-slate-500">
          <tr class="text-left">
            <th class="px-2 py-1 font-normal">时间</th>
            <th class="px-2 py-1 font-normal">方向</th>
            <th class="px-2 py-1 text-right font-normal">价格</th>
            <th class="px-2 py-1 text-right font-normal">数量</th>
            <th class="px-2 py-1 text-right font-normal">盈亏</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in trades" :key="t.id" class="border-b border-slate-800/60">
            <td class="px-2 py-1 font-mono text-slate-400">{{ t.time.slice(5) }}</td>
            <td class="px-2 py-1" :class="t.side === 'buy' ? 'text-red-400' : 'text-emerald-400'">
              {{ t.side === 'buy' ? '买' : '卖' }}
            </td>
            <td class="px-2 py-1 text-right font-mono text-slate-300">{{ t.price.toFixed(2) }}</td>
            <td class="px-2 py-1 text-right font-mono text-slate-400">{{ t.quantity }}</td>
            <td
              class="px-2 py-1 text-right font-mono"
              :class="t.realizedPnl > 0 ? 'text-red-400' : t.realizedPnl < 0 ? 'text-emerald-400' : 'text-slate-500'"
            >
              {{ t.realizedPnl === 0 ? '—' : formatSigned(t.realizedPnl) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="border-t border-slate-800 px-3 py-2 text-[11px]">
      <div class="flex justify-between">
        <span class="text-slate-400">累计已实现</span>
        <span
          class="font-mono"
          :class="totalRealized > 0 ? 'text-red-400' : totalRealized < 0 ? 'text-emerald-400' : 'text-slate-300'"
        >
          {{ formatSigned(totalRealized) }}
        </span>
      </div>
    </div>
  </div>
</template>
