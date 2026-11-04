<script setup lang="ts">
import { formatPrice, formatSigned } from '@/components/chart/scale'
import { durationText, type ClosedTrade } from '@/components/chart/trades'
import { useTradeStore } from '@/stores/trade'

/**
 * 平仓虚线的信息卡。
 *
 * 单独拆成一个组件，是因为它有两种形态，但内容必须**一字不差**：
 *   - 跟随鼠标（`pinned = false`）—— 纯展示，底部提示「停 3 秒可固定」；
 *   - 钉在画布上（`pinned = true`）—— 右上角多一个 ×，整张卡可以拖。
 *
 * 两种形态只在这个文件里写一次文案：拆成两段模板的话，改一个字段很容易只改一半
 * （比如「手数」从标题挪到行里时，跟随卡改了、钉住卡没改）。
 *
 * 内容就是需求里的六项：手数、方向（都在标题里）、开仓价、平仓价、持仓时长、已实现盈亏。
 * 盈亏由后端按 FIFO 批次用**这一批自己的开仓价**算好（见 chart/trades.ts 的说明），
 * 和上面那两个价格是同一套口径 —— 前端不参与计算，只排版。
 */
defineProps<{ trade: ClosedTrade; pinned?: boolean }>()
defineEmits<{ close: [] }>()

const store = useTradeStore()
</script>

<template>
  <div class="rounded border border-line bg-panel text-[13px] text-ink shadow-xl">
    <div class="flex items-center gap-1.5 border-b border-line px-2.5 py-1">
      <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="trade.side === 'long' ? 'bg-up' : 'bg-down'" />
      <span
        class="font-medium"
        :class="trade.side === 'long' ? 'text-up' : 'text-down'"
      >
        {{ trade.side === 'long' ? '多' : '空' }} {{ trade.quantity }} 手
      </span>
      <span class="text-[12px] text-faint">平仓</span>
      <button
        v-if="pinned"
        type="button"
        class="-mr-1 ml-auto flex h-4 w-4 items-center justify-center rounded text-[14px] leading-none text-faint hover:bg-elev hover:text-ink"
        title="关闭"
        @pointerdown.stop
        @click.stop="$emit('close')"
      >
        ×
      </button>
    </div>

    <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 px-2.5 py-1">
      <dt class="text-faint">开仓价</dt>
      <dd class="tnum text-right">{{ formatPrice(trade.openPrice, store.priceDecimals) }}</dd>
      <dt class="text-faint">平仓价</dt>
      <dd class="tnum text-right">{{ formatPrice(trade.closePrice, store.priceDecimals) }}</dd>
      <dt class="text-faint">持仓时长</dt>
      <dd class="tnum text-right">{{ durationText(trade.openTime, trade.closeTime) }}</dd>
      <dt class="text-faint">已实现盈亏</dt>
      <dd
        class="tnum text-right font-medium"
        :class="
          trade.realizedPnl > 0
            ? 'text-up'
            : trade.realizedPnl < 0
              ? 'text-down'
              : 'text-dim'
        "
      >
        {{ formatSigned(trade.realizedPnl) }}
      </dd>
    </dl>

    <div v-if="!pinned" class="border-t border-line px-2.5 py-0.5 text-[11px] text-faint">
      停 3 秒不动可固定到画布
    </div>
  </div>
</template>
