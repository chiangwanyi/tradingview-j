<script setup lang="ts">
import { computed, ref } from 'vue'

import RecordTagCell from '@/components/records/RecordTagCell.vue'
import {
  dirText,
  pnlText,
  recordTime,
  sortRows,
  sourceNameOf,
  sourceOf,
  tradeTime,
} from '@/lib/records'
import type { SortKey, TagUsage, TradeRow } from '@/lib/records'
import type { RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 交易明细列表（日历的当日详情页、统计页共用）。
 *
 * 一个刻意的选择：**整行可点，点了就进回放**。复盘时看到某一行想知道「当时是怎么
 * 进的」，最自然的手势就是点它；单独放一个只有 40px 宽的【回放】按钮，反而要瞄准。
 * 行内的标签、删除按钮都 `@click.stop`，不会误触发。
 *
 * 盈亏着色沿用全项目的语义（盈绿亏红）；来源、标签两列是 v0.21.0 新加的维度，
 * 它们和筛选是同源的（都走 lib/records.ts 的 sourceOf / hasTag）。
 */
const props = defineProps<{
  rows: TradeRow[]
  /** 全部记录里出现过的标签（打标签面板里的「已有标签」） */
  library: TagUsage[]
  /** 显示「交易时间」里的日期（跨天的明细都要：当日详情里的几笔行情可能横跨几年） */
  showDate?: boolean
  /** 空列表时显示的话 */
  emptyText?: string
}>()

const emit = defineEmits<{
  replay: [RecordingSummary]
  remove: [string]
  tags: [RecordingSummary, RecordTag[]]
}>()

const sortKey = ref<SortKey>('time')
const sortDir = ref<1 | -1>(1)
/** 删除的二次确认：记录删了没有回收站，第一下只是「上膛」 */
const armed = ref('')
let armTimer = 0

const sorted = computed(() => sortRows(props.rows, sortKey.value, sortDir.value))

function sortBy(key: SortKey): void {
  if (sortKey.value === key) {
    sortDir.value = sortDir.value === 1 ? -1 : 1
    return
  }
  sortKey.value = key
  // 时间从早到晚；盈亏 / 根数从大到小（想先看极端的那几笔）
  sortDir.value = key === 'time' ? 1 : -1
}

function arrow(key: SortKey): string {
  if (sortKey.value !== key) return ''
  return sortDir.value === 1 ? '↑' : '↓'
}

function tone(n: number): string {
  if (n > 1e-6) return 'text-up'
  if (n < -1e-6) return 'text-down'
  return 'text-ink'
}

/** 时间列：**行情**时间（入场那根 K 线）。跨天的明细要把日期带上，否则「00:05」看不出是哪天。 */
function timeText(rec: RecordingSummary): string {
  const t = tradeTime(rec)
  return props.showDate === false ? t.slice(11) || t : t
}

function exitText(rec: RecordingSummary): string {
  const exit = rec.stats.exitTime || rec.endTime
  if (!exit) return '—'
  const entry = rec.stats.entryTime
  if (entry && exit.startsWith(entry.slice(0, 10))) return exit.slice(11)
  return exit
}

function armDelete(rec: RecordingSummary): void {
  if (armed.value !== rec.id) {
    armed.value = rec.id
    if (armTimer) window.clearTimeout(armTimer)
    armTimer = window.setTimeout(() => {
      armed.value = ''
    }, 3000)
    return
  }
  armed.value = ''
  emit('remove', rec.id)
}

const th = 'whitespace-nowrap px-2.5 py-2 text-left font-medium text-faint'
const td = 'px-2.5 py-2'
</script>

<template>
  <div class="overflow-x-auto">
    <table class="w-full border-collapse text-[13px]">
      <thead class="bg-elev/60">
        <tr class="border-b border-line">
          <th :class="th">
            <button type="button" class="hover:text-accent" @click="sortBy('time')">
              行情时间 {{ arrow('time') }}
            </button>
          </th>
          <th :class="th">标的</th>
          <th :class="th">方向 / 手数</th>
          <th :class="th">入场 → 出场</th>
          <th :class="[th, 'text-right']">
            <button type="button" class="hover:text-accent" @click="sortBy('pnl')">
              盈亏 {{ arrow('pnl') }}
            </button>
          </th>
          <th :class="[th, 'text-right']">累计</th>
          <th :class="[th, 'text-right']">MFE / MAE</th>
          <th :class="[th, 'text-right']">
            <button type="button" class="hover:text-accent" @click="sortBy('bars')">
              持仓 {{ arrow('bars') }}
            </button>
          </th>
          <th :class="th">来源</th>
          <th :class="th">标签</th>
          <th :class="th">状态</th>
          <th :class="[th, 'text-right']">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in sorted"
          :key="row.rec.id"
          class="cursor-pointer border-b border-line/60 transition last:border-0 hover:bg-elev/60"
          title="点这一行进回放，逐格看当时怎么做的"
          @click="emit('replay', row.rec)"
        >
          <td :class="[td, 'tnum whitespace-nowrap']">
            <div class="text-ink">{{ timeText(row.rec) }}</div>
            <div class="text-[11px] text-faint">
              → {{ exitText(row.rec) }} · 记于 {{ recordTime(row.rec) }}
            </div>
          </td>
          <td :class="[td, 'whitespace-nowrap text-dim']">{{ row.rec.symbol }}</td>
          <td :class="[td, 'whitespace-nowrap']">
            <!-- 多 = 绿、空 = 红（和 K 线、持仓线、画布上的多空标记同一套） -->
            <span :class="row.rec.stats.side === 'long' ? 'text-up' : 'text-down'">
              {{ dirText(row.rec) }}
            </span>
            <span class="tnum ml-1 text-[11px] text-faint"
              >峰值 {{ row.rec.stats.maxQuantity }}</span
            >
          </td>
          <td :class="[td, 'tnum whitespace-nowrap text-dim']">
            {{ row.rec.stats.entryPrice || '—' }}
            <span class="text-faint">→</span>
            {{ row.rec.stats.exitPrice || '—' }}
          </td>
          <td :class="[td, 'tnum text-right font-medium whitespace-nowrap', tone(row.rec.stats.realizedPnl)]">
            {{ pnlText(row.rec.stats.realizedPnl) }}
          </td>
          <td :class="[td, 'tnum text-right whitespace-nowrap', tone(row.cum)]">
            {{ pnlText(row.cum) }}
          </td>
          <td :class="[td, 'tnum text-right whitespace-nowrap']">
            <span :class="tone(row.rec.stats.mfe)">{{ pnlText(row.rec.stats.mfe) }}</span>
            <span class="text-faint"> / </span>
            <span :class="tone(row.rec.stats.mae)">{{ pnlText(row.rec.stats.mae) }}</span>
          </td>
          <td :class="[td, 'tnum text-right whitespace-nowrap text-dim']">
            {{ row.rec.stats.barsHeld }} 根
            <span class="text-[11px] text-faint">/ {{ row.rec.stepCount }} 步</span>
          </td>
          <td :class="[td, 'whitespace-nowrap']">
            <!-- 来源：真人一种灰、机器人一种紫 —— 一眼分得出「这单是我下的还是它下的」 -->
            <span
              class="inline-flex items-center gap-1 rounded-full border px-1.5 py-px text-[11px]"
              :class="
                sourceOf(row.rec) === 'human'
                  ? 'border-line bg-elev text-dim'
                  : 'border-violet-500/30 bg-violet-500/10 text-violet-700'
              "
              :title="
                sourceOf(row.rec) === 'human'
                  ? `真人手动${row.rec.sourceName ? `（${row.rec.sourceName}）` : ''}`
                  : `机器人 · ${sourceNameOf(row.rec)}`
              "
            >
              <span
                class="h-1.5 w-1.5 rounded-full"
                :class="sourceOf(row.rec) === 'human' ? 'bg-faint' : 'bg-violet-500'"
              />
              {{ sourceNameOf(row.rec) }}
            </span>
          </td>
          <td :class="[td, 'min-w-[11rem]']">
            <RecordTagCell
              :rec="row.rec"
              :library="library"
              @change="(tags) => emit('tags', row.rec, tags)"
            />
          </td>
          <td :class="[td, 'whitespace-nowrap']">
            <span
              v-if="row.rec.stats.closed"
              class="rounded bg-elev px-1 text-[11px] text-dim"
            >
              已平仓
            </span>
            <span
              v-else
              class="rounded bg-sky-500/15 px-1 text-[11px] text-sky-600"
              title="这一笔还没平仓：记录停在最后一次操作上（可能实例被删、程序退出，或还在持仓中）"
            >
              持仓中
            </span>
          </td>
          <td :class="[td, 'text-right whitespace-nowrap']">
            <div class="flex items-center justify-end gap-1">
              <button
                type="button"
                class="rounded border border-line px-1.5 py-0.5 text-[12px] text-dim hover:border-accent hover:text-accent"
                title="回到复盘画布，逐格回放这一笔"
                @click.stop="emit('replay', row.rec)"
              >
                回放
              </button>
              <button
                type="button"
                class="rounded px-1.5 py-0.5 text-[12px]"
                :class="
                  armed === row.rec.id ? 'bg-red-600 text-white' : 'text-faint hover:text-red-600'
                "
                :title="armed === row.rec.id ? '再点一下确认删除（不可撤销）' : '删除这笔交易记录'"
                @click.stop="armDelete(row.rec)"
              >
                {{ armed === row.rec.id ? '确认删除' : '删除' }}
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="sorted.length === 0">
          <td colspan="12" class="px-3 py-10 text-center text-[14px] text-dim">
            {{ emptyText ?? '这里还没有交易记录' }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
