<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import TradePanel from '@/components/TradePanel.vue'
import { useReplayStore } from '@/stores/replay'
import type { OrderKind, Side } from '@/types/protocol'

/**
 * 模拟交易（底部「模拟交易」Tab 的内容）。
 *
 * 语义刻意保持简单（需求如此）：
 *   - 没有初始资金、没有保证金、没有手续费和滑点，只看盈亏数字和流水；
 *   - 没有止损/止盈类型 —— 想止损就挂一张 stop 单，和手动挂单完全一样；
 *   - 成交判断在后端：新揭示的那根 K 线的最高/最低价碰到挂单价就按挂单价成交。
 *
 * 布局是**横排三列**（下单 / 持仓与挂单 / 成交）：底部面板只有约 180px 高，
 * 纵向堆叠放不下，横过来正好一眼看全。
 */

const store = useReplayStore()

const side = ref<Side>('buy')
const kind = ref<OrderKind>('limit')
const price = ref<number>(0)
const manualPrice = ref(false)

const inst = computed(() => store.instance)
const currentPrice = computed(() => store.currentBar?.close ?? 0)
const priceText = computed(() =>
  currentPrice.value ? currentPrice.value.toFixed(store.priceDecimals) : '—',
)
const position = computed(() => inst.value?.position ?? { quantity: 0, side: 'flat', averagePrice: 0 })
const positionText = computed(() => {
  const p = position.value
  if (p.side === 'flat') return '空仓'
  return `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)} @ ${p.averagePrice.toFixed(store.priceDecimals)}`
})

/** 跳动用交易的 tick（没有就按两位小数） */
const step = computed(() => {
  const t = store.tick
  return t > 0 ? String(t) : '0.01'
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
  if (!(price.value > 0) || !(store.orderQuantity > 0)) return
  store.placeOrder(side.value, kind.value, price.value, store.orderQuantity)
}

const openOrders = computed(() => (inst.value?.orders ?? []).filter((o) => o.status === 'open'))
const historyOrders = computed(() =>
  (inst.value?.orders ?? [])
    .filter((o) => o.status !== 'open')
    .slice(-12)
    .reverse(),
)

function orderLabel(kindValue: string): string {
  return kindValue === 'limit' ? '限价' : '止损'
}

function sideLabel(sideValue: string): string {
  return sideValue === 'buy' ? '买' : '卖'
}

function pnlClass(v: number): string {
  if (v > 0) return 'text-emerald-600 dark:text-emerald-400'
  if (v < 0) return 'text-red-600 dark:text-red-400'
  return 'text-dim'
}

const inputClass =
  'w-full rounded border border-line bg-surface px-1.5 py-1 text-[12px] text-ink tnum focus:border-accent focus:outline-none'
</script>

<template>
  <div class="flex h-full min-h-0 divide-x divide-line">
    <!-- 下单 -->
    <div class="flex w-[16.5rem] shrink-0 flex-col gap-1.5 p-2">
      <div class="flex items-center justify-between text-[12px]">
        <span class="font-semibold">下单</span>
        <span class="tnum text-dim">现价 {{ priceText }}</span>
      </div>

      <div class="grid grid-cols-2 gap-1.5">
        <select v-model="side" :class="inputClass">
          <option value="buy">买入 / 做多</option>
          <option value="sell">卖出 / 做空</option>
        </select>
        <select v-model="kind" :class="inputClass">
          <option value="limit">限价单</option>
          <option value="stop">止损单</option>
        </select>
      </div>

      <div class="grid grid-cols-2 gap-1.5">
        <label class="block">
          <span class="text-[11px] text-faint">价格（跳动 {{ step }}）</span>
          <input
            v-model.number="price"
            type="number"
            :step="step"
            :class="inputClass"
            @input="manualPrice = true"
          />
        </label>
        <label class="block">
          <span class="text-[11px] text-faint">数量</span>
          <input v-model.number="store.orderQuantity" type="number" min="0.01" step="1" :class="inputClass" />
        </label>
      </div>

      <div class="flex items-center gap-2">
        <button type="button" class="text-[11px] text-accent hover:underline" @click="useCurrentPrice">
          用现价
        </button>
        <button
          type="button"
          class="ml-auto rounded bg-amber-600 px-3 py-1 text-[12px] font-medium text-white hover:bg-amber-500 disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!store.canOperate || !inst"
          @click="submit"
        >
          挂单
        </button>
      </div>

      <!--
        这两列加起来只有 200px 出头高（字号上调后从 184px 抬到 204px），
        左边这列没有滚动条 —— 说明文字只留一行。
        完整规则（限价买 low<=价、限价卖 high>=价、止损相反、不计手续费/滑点）在
        docs/复盘设计.md 的「模拟交易」一节，界面上放不下也不必放。
      -->
      <div class="mt-auto truncate text-[11px] leading-4 text-faint" title="限价买：最低价 ≤ 挂单价；限价卖：最高价 ≥ 挂单价；止损单相反。无资金/手续费/滑点。">
        规则见 docs/复盘设计.md · 不计手续费/滑点
      </div>
    </div>

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
