<script setup lang="ts">
import { computed } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import TradePanel from '@/components/TradePanel.vue'
import { useReplayStore } from '@/stores/replay'

/**
 * 底部「模拟交易」Tab 的内容：**持仓与挂单** + **成交流水**两列。
 *
 * 「下单」那一列（方向/单型/价格/数量/市价按钮）已经搬到 `TradeFloat.vue` ——
 * 它是要反复点的东西，浮在图上比沉在底部顺手；这里留下的是「看」的部分：
 * 持仓、盈亏、挂着的单、已经成交的流水。
 *
 * 语义仍然刻意保持简单（需求如此）：
 *   - 没有初始资金、没有保证金、没有手续费和滑点，只看盈亏数字和流水；
 *   - 没有止损/止盈类型 —— 想止损就挂一张 stop 单，和手动挂单完全一样；
 *   - 成交判断在后端：新揭示的那根 K 线的最高/最低价碰到挂单价就成交，成交价通常是挂单价，
 *     跳空（开盘就跳过挂单价）时按开盘价 —— 细节见 README「模拟交易：撮合规则与盈亏」。
 */

const store = useReplayStore()

const inst = computed(() => store.instance)
const position = computed(() => inst.value?.position ?? { quantity: 0, side: 'flat', averagePrice: 0 })
const positionText = computed(() => {
  const p = position.value
  if (p.side === 'flat') return '空仓'
  return `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)} @ ${p.averagePrice.toFixed(store.priceDecimals)}`
})

const openOrders = computed(() => (inst.value?.orders ?? []).filter((o) => o.status === 'open'))
const historyOrders = computed(() =>
  (inst.value?.orders ?? [])
    .filter((o) => o.status !== 'open')
    .slice(-12)
    .reverse(),
)

function orderLabel(kindValue: string): string {
  if (kindValue === 'limit') return '限价'
  if (kindValue === 'market') return '市价'
  return '止损'
}

function sideLabel(sideValue: string): string {
  return sideValue === 'buy' ? '买' : '卖'
}

function pnlClass(v: number): string {
  if (v > 0) return 'text-emerald-600 dark:text-emerald-400'
  if (v < 0) return 'text-red-600 dark:text-red-400'
  return 'text-dim'
}
</script>

<template>
  <div class="flex h-full min-h-0 divide-x divide-line">
    <!-- 持仓 + 挂单 -->
    <div class="flex w-[17.5rem] shrink-0 flex-col gap-1 overflow-auto p-2 text-[12px]">
      <div class="flex justify-between">
        <span class="text-dim">持仓</span>
        <span class="tnum" :class="pnlClass(position.side === 'flat' ? 0 : position.side === 'long' ? 1 : -1)">
          {{ positionText }}
        </span>
      </div>
      <div class="flex justify-between">
        <span class="text-dim">已实现盈亏</span>
        <span class="tnum" :class="pnlClass(inst?.realizedPnl ?? 0)">
          {{ formatSigned(inst?.realizedPnl ?? 0) }}
        </span>
      </div>
      <div class="flex justify-between">
        <span class="text-dim">浮动盈亏</span>
        <span class="tnum" :class="pnlClass(inst?.unrealizedPnl ?? 0)">
          {{ formatSigned(inst?.unrealizedPnl ?? 0) }}
        </span>
      </div>

      <div class="mt-1 flex items-center justify-between border-t border-line pt-1 text-[11px] text-faint">
        <span>挂单中 {{ openOrders.length }}</span>
        <span>最近 {{ historyOrders.length }}</span>
      </div>

      <div v-if="openOrders.length === 0" class="text-[11px] text-faint">没有挂单</div>
      <div v-for="o in openOrders" :key="o.id" class="flex items-center gap-1.5 text-[11px]">
        <span :class="o.side === 'buy' ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">
          {{ sideLabel(o.side) }}{{ orderLabel(o.kind) }}
        </span>
        <span class="tnum">{{ o.price.toFixed(store.priceDecimals) }}</span>
        <span class="text-faint">×{{ o.quantity }}</span>
        <button
          type="button"
          class="ml-auto text-faint hover:text-red-500 disabled:opacity-40"
          :disabled="!store.canOperate"
          @click="store.cancelOrder(o.id)"
        >
          撤单
        </button>
      </div>

      <div v-for="o in historyOrders" :key="o.id" class="flex items-center gap-1.5 text-[11px] text-faint">
        <span>{{ sideLabel(o.side) }}{{ orderLabel(o.kind) }}</span>
        <span class="tnum">{{ o.price.toFixed(store.priceDecimals) }}</span>
        <span class="ml-auto">{{ o.status === 'filled' ? '已成交' : '已撤销' }}</span>
      </div>
    </div>

    <!-- 成交 -->
    <div class="min-w-0 flex-1">
      <TradePanel />
    </div>
  </div>
</template>
