<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { WEEKDAYS, monthGrid, monthLabel, monthOf, shiftMonth } from '@/lib/calendar'
import { money, signedMoney } from '@/lib/account'
import type { AccountDay } from '@/types/protocol'

/**
 * 交易日历（考核账户详情页）。
 *
 * 和【交易记录】那块日历**不是一回事**：那边的格子是「我哪天做了这笔单」，
 * 这边是「行情走到了哪一天」—— 所以一格背后是一整个交易日（日文件那一天），
 * 有 K 线走过就有格子，哪怕一笔没做（灰格子 = 那天没下手）。
 *
 * 切天由后端做（`AccountDTO.Days`，判据是 barDate，和日终结算同一把尺子）。
 * 这里只排版：**绝不按 `time.slice(0,10)` 自己分组** —— 美盘 06:00 到次日 05:00
 * 是一个交易日，前端按日期切会把它们劈成两天，日历上的数就和余额对不上了。
 *
 * 配色沿用全项目那套（绿 = 盈利、红 = 亏损），亏损格带 ▼、盈利格带 ▲ ——
 * 只靠颜色的话，色觉障碍的人读不出来。
 */

const props = defineProps<{
  /** 后端的交易日历（已按时间升序） */
  days: AccountDay[]
  /** 当前交易日（`account.dayDate`），用来决定初始显示哪个月、哪一格画「当前」 */
  current?: string
}>()

const moneyCls = (v: number): string =>
  v > 1e-6 ? 'text-up' : v < -1e-6 ? 'text-down' : 'text-dim'

/** 日期 → 那一天。格子只查这一份，模板里不再现算。 */
const byDate = computed(() => {
  const m = new Map<string, AccountDay>()
  for (const d of props.days) m.set(d.date, d)
  return m
})

/** 有数据的月份区间：翻页不越界（越界只会翻出一片空白，没有意义） */
const range = computed(() => {
  if (props.days.length === 0) return { first: '', last: '' }
  return { first: monthOf(props.days[0].date), last: monthOf(props.days[props.days.length - 1].date) }
})

/** 默认停在「当前交易日」那个月 —— 打开面板就该看见今天在哪儿 */
const month = ref(monthOf(props.current || props.days[props.days.length - 1]?.date || ''))

watch(
  () => props.current,
  (cur) => {
    if (cur) month.value = monthOf(cur)
  },
)

function canShift(delta: number): boolean {
  const next = shiftMonth(month.value, delta)
  return next >= range.value.first && next <= range.value.last
}

const navCls =
  'flex h-6 w-6 items-center justify-center rounded border border-line text-[13px] leading-none text-dim transition hover:border-accent hover:text-accent disabled:opacity-30 disabled:hover:border-line disabled:hover:text-dim'

/** 本月汇总（只算属于这个月的格子） */
const monthStats = computed(() => {
  let pnl = 0
  let trades = 0
  let up = 0
  let down = 0
  let flat = 0
  for (const d of props.days) {
    if (monthOf(d.date) !== month.value) continue
    pnl += d.pnl
    trades += d.trades
    if (d.pnl > 1e-6) up++
    else if (d.pnl < -1e-6) down++
    else flat++
  }
  return { pnl, trades, up, down, flat, days: up + down + flat }
})

const cells = computed(() =>
  monthGrid(month.value, props.current || undefined).map((cell) => {
    const rec = byDate.value.get(cell.day) ?? null
    let cls = 'border-transparent bg-transparent text-transparent'
    if (cell.inMonth) {
      if (!rec) cls = 'border-line/60 bg-elev/40 text-faint'
      else if (rec.pnl > 1e-6) cls = 'border-up/30 bg-up-soft text-up'
      else if (rec.pnl < -1e-6) cls = 'border-down/30 bg-down-soft text-down'
      else cls = 'border-line bg-elev text-dim'
    }
    if (rec?.current) cls += ' ring-2 ring-accent/50'
    const tip = !cell.inMonth
      ? ''
      : rec
        ? `${rec.date} · ${rec.trades} 笔 · ${signedMoney(rec.pnl)}`
        : `${cell.day} · 没走到`
    const arrow = rec ? (rec.pnl > 1e-6 ? '▲' : rec.pnl < -1e-6 ? '▼' : '···') : ''
    return { ...cell, rec, cls, tip, arrow }
  }),
)

const today = computed(() => props.days.find((d) => d.current) ?? null)
</script>

<template>
  <div class="flex h-full min-h-0 gap-3 overflow-hidden px-2 py-2">
    <div v-if="days.length === 0" class="flex flex-1 items-center justify-center text-[13px] text-faint">
      这个账户还没走过一根 K 线，日历是空的。
    </div>

    <template v-else>
      <!-- 月历：格子 = 交易日，颜色 = 那天赚没赚 -->
      <div class="flex min-h-0 w-[19rem] shrink-0 flex-col">
        <div class="mb-1 flex items-center gap-1.5">
          <button
            type="button"
            :class="navCls"
            :disabled="!canShift(-1)"
            title="上个月"
            @click="month = shiftMonth(month, -1)"
          >
            ‹
          </button>
          <span class="tnum text-[13px] font-medium">{{ monthLabel(month) }}</span>
          <button
            type="button"
            :class="navCls"
            :disabled="!canShift(1)"
            title="下个月"
            @click="month = shiftMonth(month, 1)"
          >
            ›
          </button>
          <span class="ml-auto tnum text-[12px] text-dim">
            {{ monthStats.days }} 个交易日 · {{ monthStats.trades }} 笔
          </span>
          <span class="tnum text-[12px] font-medium" :class="moneyCls(monthStats.pnl)">
            {{ signedMoney(monthStats.pnl) }}
          </span>
        </div>

        <div class="grid grid-cols-7 gap-1 text-center text-[11px] text-faint">
          <span v-for="w in WEEKDAYS" :key="w">{{ w }}</span>
        </div>
        <div class="mt-1 grid min-h-0 flex-1 grid-cols-7 gap-1 overflow-y-auto">
          <div
            v-for="c in cells"
            :key="c.day"
            class="flex items-center justify-center gap-0.5 rounded border px-0.5 py-0.5 text-[11px] leading-tight"
            :class="c.cls"
            :title="c.tip"
          >
            <span class="tnum">{{ c.date }}</span>
            <span v-if="c.arrow" class="text-[9px]">{{ c.arrow }}</span>
          </div>
        </div>
      </div>

      <!-- 右侧：本月盈亏分布 + 当前这一天 -->
      <div class="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto border-l border-line pl-3">
        <div>
          <div class="text-[11px] text-faint">这一格是「行情走到的那一天」，不是今天</div>
          <div class="mt-1 flex gap-3 text-[12px]">
            <span class="text-up">盈利 {{ monthStats.up }} 天</span>
            <span class="text-down">亏损 {{ monthStats.down }} 天</span>
            <span class="text-dim">没下手 {{ monthStats.flat }} 天</span>
          </div>
        </div>

        <div v-if="today" class="rounded border border-line bg-elev/40 px-2 py-1.5">
          <div class="flex items-center gap-2">
            <span class="text-[12px] font-medium">{{ today.date }}</span>
            <span class="rounded bg-accent-soft px-1 text-[10px] text-accent">当前交易日</span>
            <span class="tnum ml-auto text-[13px] font-semibold" :class="moneyCls(today.pnl)">
              {{ signedMoney(today.pnl) }}
            </span>
          </div>
          <div class="tnum mt-1 flex flex-wrap gap-x-3 text-[11px] text-dim">
            <span>{{ today.trades }} 笔</span>
            <span>{{ today.start.slice(11) }} ~ {{ today.end.slice(11) }}</span>
            <span>已走 {{ today.bars }} 根</span>
            <span>累计盈亏 {{ money(today.cum) }}</span>
          </div>
        </div>

        <!-- 最近几个有交易的日子：日历只看盈亏，这里能看见「哪天干了多少」 -->
        <div class="min-h-0">
          <div class="mb-1 text-[11px] text-faint">最近交易日</div>
          <div class="space-y-0.5">
            <div
              v-for="d in [...days].reverse().slice(0, 6)"
              :key="d.date"
              class="flex items-center gap-2 rounded px-1 py-0.5 text-[12px] hover:bg-elev"
            >
              <span class="tnum text-dim">{{ d.date.slice(5) }}</span>
              <span class="text-faint">{{ d.trades }} 笔</span>
              <span class="tnum ml-auto" :class="moneyCls(d.pnl)">{{ signedMoney(d.pnl) }}</span>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
