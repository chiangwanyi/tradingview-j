<script setup lang="ts">
import { computed } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import TradePanel from '@/components/TradePanel.vue'
import { money, phaseClass, phaseLabel, signedMoney } from '@/lib/account'
import { useReplayStore } from '@/stores/replay'

/**
 * 底部「模拟交易」Tab 的内容：**持仓与挂单** + **成交流水**两列。
 *
 * 「下单」那一列（方向/单型/价格/数量/市价按钮）已经搬到 `TradeFloat.vue` ——
 * 它是要反复点的东西，浮在图上比沉在底部顺手；这里留下的是「看」的部分：
 * 持仓、盈亏、挂着的单、已经成交的流水。
 *
 * 语义仍然刻意保持简单（需求如此）：
 *   - 自由模式没有初始资金、没有保证金、没有滑点；
 *   - **手续费是全局的**（v0.31.0）：按品种、按成交边收，已经算进「已实现盈亏」里，
 *     所以那个数字是净额；
 *   - 考核账户多一块「余额 / 权益 / 亏损线 / 当日净额」，数字全部由后端算好；
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

/**
 * 考核账户（undefined = 自由模式）。
 *
 * 回放里永远是 undefined：`account_changed` 不进交易记录的步骤白名单，记录也只覆盖
 * 「持仓期间」。所以这里不用另外判 playbackActive —— 没数据就不显示。
 */
const account = computed(() => inst.value?.account)

/** EVAL 还差多少到目标（用后端给的 target 减余额，前端不做别的算术）。 */
const targetGap = computed(() => {
  const a = account.value
  if (!a) return 0
  return a.target - a.balance
})

/**
 * 一行状态提示：为什么要等、为什么不能下单。
 *
 * 优先级从「不能交易」到「提示」：失败 > 已出金 > 当日封顶 > 等平仓切 FUNDED。
 */
const accountNotice = computed(() => {
  const a = account.value
  if (!a) return ''
  if (a.phase === 'breached') return `考核失败：${a.failReason ?? '权益触及亏损线'}`
  if (a.phase === 'payout') return `考核已结束，可出金 ${money(a.payout ?? 0)}`
  if (a.dayLocked) return `当日净盈利已达上限 ${money(a.rules.dailyLimit)}，今天不能再开仓`
  if (a.waitingFund) return 'EVAL 已达标：平掉持仓后进入 FUNDED'
  return ''
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

// 绿赚红亏（美股习惯）：和画布、交易记录页、统计图同一套语义色
function pnlClass(v: number): string {
  if (v > 0) return 'text-up'
  if (v < 0) return 'text-down'
  return 'text-dim'
}
</script>

<template>
  <div class="flex h-full min-h-0 divide-x divide-line">
    <!-- 持仓 + 挂单 -->
    <div class="flex w-[19.5rem] shrink-0 flex-col gap-1.5 overflow-auto p-2.5 text-[13px]">
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

      <!-- 考核账户 -->
      <template v-if="account">
        <div class="mt-1 flex items-center justify-between border-t border-line pt-1.5">
          <span class="flex items-center gap-1.5">
            <span class="rounded px-1 py-px text-[11px] font-medium" :class="phaseClass(account.phase)">
              {{ phaseLabel(account.phase) }}
            </span>
            <span class="text-[12px] text-faint">{{ account.rules.name }}</span>
          </span>
          <span v-if="account.locked" class="text-[11px] text-faint">亏损线已锁定</span>
        </div>
        <div class="flex justify-between">
          <span class="text-dim">余额</span>
          <span class="tnum">{{ money(account.balance) }}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-dim">权益</span>
          <span class="tnum" :class="pnlClass(account.equity - account.balance)">
            {{ money(account.equity) }}
          </span>
        </div>
        <div class="flex justify-between">
          <span class="text-dim">亏损线</span>
          <span class="tnum text-down">{{ money(account.floor) }}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-dim">当日净额</span>
          <span class="tnum" :class="pnlClass(account.dayNet)">{{ signedMoney(account.dayNet) }}</span>
        </div>
        <div v-if="account.phase === 'eval'" class="flex justify-between">
          <span class="text-dim">距目标</span>
          <span class="tnum" :class="targetGap <= 0 ? 'text-up' : ''">{{ money(targetGap) }}</span>
        </div>
        <div v-else-if="account.phase !== 'breached'" class="flex justify-between">
          <span class="text-dim">盈利日</span>
          <span class="tnum">{{ account.profitDays }} / {{ account.rules.profitDays }}</span>
        </div>
        <div
          v-if="accountNotice"
          class="rounded px-1.5 py-1 text-[12px]"
          :class="
            account.phase === 'breached'
              ? 'bg-red-500/10 text-red-600'
              : 'bg-amber-500/10 text-amber-700'
          "
        >
          {{ accountNotice }}
        </div>
      </template>

      <div class="mt-1 flex items-center justify-between border-t border-line pt-1 text-[12px] text-faint">
        <span>挂单中 {{ openOrders.length }}</span>
        <span>最近 {{ historyOrders.length }}</span>
      </div>

      <div v-if="openOrders.length === 0" class="text-[12px] text-faint">没有挂单</div>
      <div v-for="o in openOrders" :key="o.id" class="flex items-center gap-1.5 text-[12px]">
        <span :class="o.side === 'buy' ? 'text-up' : 'text-down'">
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

      <div v-for="o in historyOrders" :key="o.id" class="flex items-center gap-1.5 text-[12px] text-faint">
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
