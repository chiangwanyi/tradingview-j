<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import { useReplayStore } from '@/stores/replay'
import type { OrderKind, Side } from '@/types/protocol'

/**
 * 模拟交易面板：挂单、撤单、持仓与盈亏。
 *
 * 语义刻意保持简单（需求如此）：
 *   - 没有初始资金、没有保证金、没有手续费和滑点，只看盈亏数字和流水；
 *   - 没有止损/止盈类型 —— 想止损就挂一张 stop 单，和手动挂单完全一样；
 *   - 成交判断在后端：新揭示的那根 K 线的最高/最低价碰到挂单价就按挂单价成交。
 */

const store = useReplayStore()

const side = ref<Side>('buy')
const kind = ref<OrderKind>('limit')
const price = ref<number>(0)
const quantity = ref<number>(1)
const manualPrice = ref(false)

const inst = computed(() => store.instance)
const currentPrice = computed(() => store.currentBar?.close ?? 0)
const position = computed(() => inst.value?.position ?? { quantity: 0, side: 'flat', averagePrice: 0 })
const positionText = computed(() => {
  const p = position.value
  if (p.side === 'flat') return '空仓'
  return `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)} @ ${p.averagePrice.toFixed(2)}`
})

// 换实例、或者行情往前走而用户没手动改过价格时，价格框跟着现价
watch(
  () => inst.value?.id,
  () => {
    manualPrice.value = false
    price.value = round2(currentPrice.value)
  },
  { immediate: true },
)
watch(currentPrice, (v) => {
  if (!manualPrice.value && v > 0) price.value = round2(v)
})

function round2(v: number): number {
  return Math.round(v * 100) / 100
}

function useCurrentPrice(): void {
  manualPrice.value = false
  price.value = round2(currentPrice.value)
}

function submit(): void {
  if (!inst.value || !store.canOperate) return
  if (!(price.value > 0) || !(quantity.value > 0)) return
  store.placeOrder(side.value, kind.value, price.value, quantity.value)
}

const openOrders = computed(() => (inst.value?.orders ?? []).filter((o) => o.status === 'open'))
const historyOrders = computed(() =>
  (inst.value?.orders ?? []).filter((o) => o.status !== 'open').slice(-8).reverse(),
)

function orderLabel(kindValue: string): string {
  return kindValue === 'limit' ? '限价' : '止损'
}

function sideLabel(sideValue: string): string {
  return sideValue === 'buy' ? '买' : '卖'
}
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="flex items-center justify-between border-b border-slate-800 px-3 py-2">
      <span class="text-xs font-semibold tracking-wide text-slate-300">模拟交易</span>
      <span class="font-mono text-[11px] text-slate-400">
        现价 {{ currentPrice ? currentPrice.toFixed(2) : '—' }}
      </span>
    </div>

    <div class="border-b border-slate-800 px-3 py-2 text-[11px]">
      <div class="flex justify-between">
        <span class="text-slate-400">持仓</span>
        <span class="font-mono" :class="position.side === 'flat' ? 'text-slate-400' : position.side === 'long' ? 'text-red-400' : 'text-emerald-400'">
          {{ positionText }}
        </span>
      </div>
      <div class="mt-1 flex justify-between">
        <span class="text-slate-400">已实现盈亏</span>
        <span
          class="font-mono"
          :class="(inst?.realizedPnl ?? 0) > 0 ? 'text-red-400' : (inst?.realizedPnl ?? 0) < 0 ? 'text-emerald-400' : 'text-slate-400'"
        >
          {{ formatSigned(inst?.realizedPnl ?? 0) }}
        </span>
      </div>
      <div class="mt-1 flex justify-between">
        <span class="text-slate-400">浮动盈亏</span>
        <span
          class="font-mono"
          :class="(inst?.unrealizedPnl ?? 0) > 0 ? 'text-red-400' : (inst?.unrealizedPnl ?? 0) < 0 ? 'text-emerald-400' : 'text-slate-400'"
        >
          {{ formatSigned(inst?.unrealizedPnl ?? 0) }}
        </span>
      </div>
      <div class="mt-1 text-[10px] text-slate-500">无初始资金 · 不计手续费/滑点/点差</div>
    </div>

    <div class="space-y-2 border-b border-slate-800 p-3" :class="store.canOperate ? '' : 'opacity-60'">
      <div class="grid grid-cols-2 gap-2">
        <select v-model="side" class="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200">
          <option value="buy">买入 / 做多</option>
          <option value="sell">卖出 / 做空</option>
        </select>
        <select v-model="kind" class="rounded border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200">
          <option value="limit">限价单</option>
          <option value="stop">止损单</option>
        </select>
      </div>
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="text-[10px] text-slate-500">价格</span>
          <input
            v-model.number="price"
            type="number"
            step="0.25"
            class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 font-mono text-xs text-slate-200"
            @input="manualPrice = true"
          />
        </label>
        <label class="block">
          <span class="text-[10px] text-slate-500">数量</span>
          <input
            v-model.number="quantity"
            type="number"
            min="0.01"
            step="1"
            class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 font-mono text-xs text-slate-200"
          />
        </label>
      </div>
      <div class="flex items-center gap-2">
        <button type="button" class="text-[10px] text-sky-400 hover:text-sky-300" @click="useCurrentPrice">
          用现价
        </button>
        <button
          type="button"
          class="ml-auto rounded bg-amber-600 px-3 py-1 text-xs font-medium text-white hover:bg-amber-500 disabled:cursor-not-allowed disabled:bg-slate-700"
          :disabled="!store.canOperate || !inst"
          @click="submit"
        >
          挂单
        </button>
      </div>
      <div class="text-[10px] text-slate-500">
        限价买单在最低价 ≤ 挂单价时成交；卖单在最高价 ≥ 挂单价时成交。止损单相反。
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-auto">
      <div class="px-3 pt-2 text-[11px] text-slate-400">挂单中（{{ openOrders.length }}）</div>
      <div v-if="openOrders.length === 0" class="px-3 py-1 text-[10px] text-slate-600">没有挂单</div>
      <div
        v-for="o in openOrders"
        :key="o.id"
        class="flex items-center gap-2 border-b border-slate-800/60 px-3 py-1.5 text-[11px]"
      >
        <span :class="o.side === 'buy' ? 'text-red-400' : 'text-emerald-400'">
          {{ sideLabel(o.side) }}{{ orderLabel(o.kind) }}
        </span>
        <span class="font-mono text-slate-300">{{ o.price.toFixed(2) }}</span>
        <span class="text-slate-500">×{{ o.quantity }}</span>
        <button
          type="button"
          class="ml-auto text-[10px] text-slate-500 hover:text-red-400 disabled:text-slate-700"
          :disabled="!store.canOperate"
          @click="store.cancelOrder(o.id)"
        >
          撤单
        </button>
      </div>

      <div class="px-3 pt-3 text-[11px] text-slate-400">最近订单</div>
      <div v-if="historyOrders.length === 0" class="px-3 py-1 text-[10px] text-slate-600">还没有已结束的订单</div>
      <div v-for="o in historyOrders" :key="o.id" class="border-b border-slate-800/60 px-3 py-1.5 text-[10px]">
        <div class="flex items-center gap-2">
          <span :class="o.side === 'buy' ? 'text-red-400' : 'text-emerald-400'">
            {{ sideLabel(o.side) }}{{ orderLabel(o.kind) }}
          </span>
          <span class="font-mono text-slate-300">{{ o.price.toFixed(2) }}</span>
          <span class="ml-auto" :class="o.status === 'filled' ? 'text-sky-300' : 'text-slate-500'">
            {{ o.status === 'filled' ? '已成交' : '已撤销' }}
          </span>
        </div>
        <div class="mt-0.5 font-mono text-slate-600">
          {{ o.placedAt }}{{ o.filledAt ? ` → ${o.filledAt}` : '' }}
        </div>
      </div>
    </div>
  </div>
</template>
