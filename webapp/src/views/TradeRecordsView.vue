<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { formatSigned } from '@/components/chart/scale'
import Icon from '@/components/Icon.vue'
import PagerBar from '@/components/PagerBar.vue'
import { errorText } from '@/data/api'
import { clearTradeData, tradeRecords } from '@/data/history'
import type { ClearedTradeData, TradeRow } from '@/data/history'

/**
 * 交易记录：`trade_record` 的全量分页浏览（只读）+ 一个清空按钮。
 *
 * 只列摘要（方向 / 手数 / 进出价 / 毛利 / 手续费 / 净额 / 是否已平），不含每一步明细 ——
 * 回放在交易页，这里是「回头看我一共做了哪些单」。跨账户，按开仓时间倒序。
 *
 * 毛利、手续费、净额三列都由后端算好发过来（{@code TradeRecordView}），前端不自己相减：
 * 反手时一笔成交横跨两条记录，手续费的切分只有后端知道该按哪条腿算。
 *
 * 清空（`DELETE /api/history/trade-data`）删的是**交易信息那 4 张表**，不止这个列表：
 * 挂单、成交、交易记录、记录标签一起走。和操作记录页的清空是两个独立入口 ——
 * 那边删的是「按过哪些按钮」，这边删的是「做过哪些单」。同样两步确认，因为不可逆。
 */

const SIZE = 50

const page = ref(0)
const total = ref(0)
const rows = ref<TradeRow[]>([])
const loading = ref(true)
const error = ref('')
const notice = ref('')

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

// ---- 清空所有交易信息（两步确认）----
const armed = ref(false)
const clearing = ref(false)

/** 后端分表报数 → 一句人话。4 个数都列出来，用户才能核对删的范围对不对。 */
function clearedText(c: ClearedTradeData): string {
  const total = c.orders + c.trades + c.records + c.tagLinks
  return `已清空 ${total} 行（挂单 ${c.orders} / 成交 ${c.trades} / 交易记录 ${c.records} / 标签关联 ${c.tagLinks}）`
}

function onClearClick(): void {
  if (!armed.value) {
    armed.value = true
    return
  }
  armed.value = false
  clearing.value = true
  notice.value = ''
  clearTradeData()
    .then((c) => {
      notice.value = clearedText(c)
      page.value = 0
      return load()
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      clearing.value = false
    })
}

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
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-[26px] font-semibold tracking-tight text-ink">交易记录</h1>
          <p class="mt-1 text-[13px] text-dim">
            所有账户的每一笔交易，按开仓时间倒序。毛利不扣手续费；手续费按平仓腿 / 开仓腿分摊，净额 = 毛利 − 手续费。
          </p>
        </div>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md border px-3 py-2 text-[13px] font-medium transition-colors disabled:cursor-default disabled:opacity-50"
          :class="
            armed
              ? 'border-red-300 bg-red-50 text-red-700 hover:bg-red-100'
              : 'border-line text-dim hover:bg-elev'
          "
          :disabled="clearing"
          @click="onClearClick"
          @blur="armed = false"
        >
          <Icon name="trash" :size="15" />
          {{
            clearing
              ? '清空中…'
              : armed
                ? '再点一下确认清空（挂单 / 成交 / 记录一起删，不可撤销）'
                : '清空所有交易信息'
          }}
        </button>
      </div>
    </header>

    <p
      v-if="error"
      class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
    >
      {{ error }}
    </p>

    <p
      v-if="notice"
      class="mx-8 mb-3 rounded-md border border-line bg-elev px-3 py-2 text-[12px] text-dim"
    >
      {{ notice }}。操作记录、账户、标签本子、图形、语音、K 线都没有动；已经打开的交易页要刷新才会看到空盘面。
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
              <th class="px-3 py-2 text-right font-normal">毛利</th>
              <th class="px-3 py-2 text-right font-normal">手续费</th>
              <th class="px-3 py-2 text-right font-normal">净额</th>
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
              <td class="tnum px-3 py-1.5 text-right text-dim">
                {{ r.commission == null ? '—' : '-' + r.commission.toFixed(2) }}
              </td>
              <td class="tnum px-3 py-1.5 text-right" :class="pnlClass(r.netPnl ?? 0)">
                {{ r.netPnl == null ? '—' : formatSigned(r.netPnl) }}
              </td>
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
