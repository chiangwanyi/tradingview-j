<script setup lang="ts">
import { computed } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import { useReplayStore } from '@/stores/replay'

/**
 * 成交流水（底部「模拟交易」Tab 的右列）。
 *
 * 每一笔都对应一张被触发的挂单，`realizedPnl` 是这一笔平掉的部分实现的盈亏
 * （开仓/加仓时为 0）—— 这是后端算好的，前端不重算，避免出现两份口径。
 */

const store = useReplayStore()

const trades = computed(() => (store.instance?.trades ?? []).slice().reverse())

function pnlClass(v: number): string {
  if (v > 0) return 'text-emerald-600 dark:text-emerald-400'
  if (v < 0) return 'text-red-600 dark:text-red-400'
  return 'text-faint'
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <!-- 累计已实现盈亏放在左边「持仓」那一列（同一个数字只在一处显示） -->
    <div class="flex items-center justify-between px-2.5 py-1.5 text-[13px] text-faint">
      <span>成交记录 {{ trades.length }} 笔</span>
      <span>盈亏 = 平掉那部分实现的</span>
    </div>

    <div v-if="trades.length === 0" class="px-2.5 py-1 text-[13px] text-faint">
      还没有成交。挂一张单，然后点「下一根」让它有机会被触发。
    </div>

    <div v-else class="min-h-0 flex-1 overflow-auto">
      <table class="w-full text-[13px]">
        <thead class="sticky top-0 bg-elev text-faint">
          <tr class="text-left">
            <th class="px-2.5 py-1.5 font-normal">时间</th>
            <th class="px-2.5 py-1.5 font-normal">方向</th>
            <th class="px-2.5 py-1.5 text-right font-normal">价格</th>
            <th class="px-2.5 py-1.5 text-right font-normal">数量</th>
            <th class="px-2.5 py-1.5 text-right font-normal">盈亏</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in trades" :key="t.id" class="border-b border-line/60">
            <td class="tnum px-2.5 py-1 text-dim">{{ t.time.slice(5) }}</td>
            <td
              class="px-2.5 py-1"
              :class="t.side === 'buy' ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"
            >
              {{ t.side === 'buy' ? '买' : '卖' }}
            </td>
            <td class="tnum px-2.5 py-1 text-right">{{ t.price.toFixed(store.priceDecimals) }}</td>
            <td class="tnum px-2.5 py-1 text-right text-dim">{{ t.quantity }}</td>
            <td class="tnum px-2.5 py-1 text-right" :class="pnlClass(t.realizedPnl)">
              {{ t.realizedPnl === 0 ? '—' : formatSigned(t.realizedPnl) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
