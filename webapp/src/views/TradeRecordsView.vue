<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import PagerBar from '@/components/PagerBar.vue'
import { errorText } from '@/data/api'
import { tradeRecords } from '@/data/history'
import type { TradeRow } from '@/data/history'

/**
 * 交易记录：`trade_record` 的全量分页浏览（只读）。
 *
 * 只列摘要（方向 / 手数 / 进出价 / 毛利 / 持仓根数 / 是否已平），不含每一步明细 ——
 * 回放在交易页，这里是「回头看我一共做了哪些单」。跨账户，按开仓时间倒序。
 */

const SIZE = 50

const page = ref(0)
const total = ref(0)
const rows = ref<TradeRow[]>([])
const loading = ref(true)
const error = ref('')

function load(): Promise<void> {
  loading.value = true
  return tradeRecords(page.value, SIZE)
    .then((p) => {
      rows.value = p.items
      total.value = p.total
      error.value = ''
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
    })
}

function go(p: number): void {
  page.value = p
  load()
}

onMounted(load)

/** 绿赚红亏（美股口径），和持仓面板、成交流水同一套语义色。 */
function pnlClass(v: number): string {
  if (v > 0) return 'text-up'
  if (v < 0) return 'text-down'
  return 'text-faint'
}

const symbolTail = (id: string): string => id.split(':').pop() ?? id
const sideLabel = (s?: string): string => (s === 'buy' ? '买' : s === 'sell' ? '卖' : '—')
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-10 pb-4">
      <h1 class="text-[26px] font-semibold tracking-tight text-ink">交易记录</h1>
      <p class="mt-1 text-[13px] text-dim">
        所有账户的每一笔交易，按开仓时间倒序。盈亏是毛利（没扣手续费）。
      </p>
    </header>

    <p
      v-if="error"
      class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
    >
      {{ error }}
    </p>

    <div class="flex min-h-0 flex-1 flex-col px-8 pb-10">
      <div class="min-h-0 flex-1 overflow-auto rounded-lg border border-line bg-panel">
        <table class="w-full text-[13px]">
          <thead class="sticky top-0 bg-elev text-left text-faint">
            <tr>
              <th class="px-3 py-2 font-normal">账户</th>
              <th class="px-3 py-2 font-normal">标的</th>
              <th class="px-3 py-2 font-normal">方向</th>
              <th class="px-3 py-2 font-normal">开仓</th>
              <th class="px-3 py-2 font-normal">平仓</th>
              <th class="px-3 py-2 text-right font-normal">手数</th>
              <th class="px-3 py-2 text-right font-normal">开仓价</th>
              <th class="px-3 py-2 text-right font-normal">平仓价</th>
              <th class="px-3 py-2 text-right font-normal">盈亏</th>
              <th class="px-3 py-2 text-right font-normal">根数</th>
              <th class="px-3 py-2 font-normal">状态</th>
              <th class="px-3 py-2 font-normal">来源</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.id" class="border-t border-line/60">
              <td class="px-3 py-1.5 text-ink">{{ r.accountName ?? '—' }}</td>
              <td class="px-3 py-1.5 text-dim">{{ symbolTail(r.symbolId) }}</td>
              <td class="px-3 py-1.5" :class="r.side === 'buy' ? 'text-up' : r.side ? 'text-down' : 'text-faint'">
                {{ sideLabel(r.side) }}
              </td>
              <td class="tnum px-3 py-1.5 text-dim">{{ r.entryBarTime?.slice(5) ?? '—' }}</td>
              <td class="tnum px-3 py-1.5 text-dim">{{ r.exitBarTime?.slice(5) ?? '—' }}</td>
              <td class="tnum px-3 py-1.5 text-right text-dim">{{ r.quantity ?? '—' }}</td>
              <td class="tnum px-3 py-1.5 text-right text-dim">
                {{ r.entryPrice == null ? '—' : r.entryPrice.toFixed(2) }}
              </td>
              <td class="tnum px-3 py-1.5 text-right text-dim">
                {{ r.exitPrice == null ? '—' : r.exitPrice.toFixed(2) }}
              </td>
              <td class="tnum px-3 py-1.5 text-right" :class="pnlClass(r.realizedPnl ?? 0)">
                {{ r.realizedPnl == null ? '—' : formatSigned(r.realizedPnl) }}
              </td>
              <td class="tnum px-3 py-1.5 text-right text-faint">{{ r.barsHeld }}</td>
              <td class="px-3 py-1.5" :class="r.closed ? 'text-dim' : 'text-accent-ink'">
                {{ r.closed ? '已平' : '未平' }}
              </td>
              <td class="px-3 py-1.5 text-faint">{{ r.actor === 'bot' ? '机器人' : '真人' }}</td>
            </tr>
          </tbody>
        </table>

        <p v-if="!rows.length" class="px-3 py-16 text-center text-[13px] text-faint">
          <template v-if="loading">加载中…</template>
          <template v-else>还没有交易记录。开一笔仓再平掉，这里就有了。</template>
        </p>
      </div>

      <PagerBar :page="page" :size="SIZE" :total="total" @go="go" />
    </div>
  </section>
</template>
