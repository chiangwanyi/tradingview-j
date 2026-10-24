<script setup lang="ts">
import { computed } from 'vue'

import { WEEKDAYS, monthGrid, monthLabel, monthOf, shiftMonth } from '@/lib/calendar'
import { moneyShort } from '@/lib/records'
import type { DayStats } from '@/lib/records'

/**
 * 交易日历（月视图）。
 *
 * 一台「每天赚了多少」的仪表：**有交易的日子按盈亏染色，没交易的日子置灰**。
 * 灰不是「没有数据」，而是「那天我没练」—— 这正是复盘时最该一眼看见的东西，
 * 所以空日子不给任何数字，只给一块浅灰的底。
 *
 * 配色沿用全项目那一套（绿 = 盈利、红 = 亏损），和 K 线、列表、统计图完全一致；
 * 亏损日同时带一个 ▼、盈利日带 ▲ —— 只靠颜色的话，色觉障碍的人读不出来，
 * 灰度打印也会糊成一片。
 *
 * 组件本身不碰数据：`days` 是父页面按来源筛选之后算好的按天汇总，
 * 点某一天只 emit 一个日期，跳不跳、怎么跳由页面决定。
 */
const props = defineProps<{
  /** `YYYY-MM` */
  month: string
  /** 按天汇总（只含筛选后的记录） */
  days: Map<string, DayStats>
  /** 今天的 `YYYY-MM-DD`（由页面传，整块日历只有这一个「今天」） */
  today: string
}>()

const emit = defineEmits<{
  'update:month': [string]
  select: [string]
}>()

const navCls =
  'flex h-7 w-7 items-center justify-center rounded border border-line text-[15px] leading-none text-dim transition hover:border-accent hover:text-accent'

/** 本月（只算属于这个月的格子）的汇总，写在标题右边 */
const summary = computed(() => {
  let count = 0
  let pnl = 0
  let upDays = 0
  let downDays = 0
  for (const [day, d] of props.days) {
    if (monthOf(day) !== props.month) continue
    count += d.count
    pnl += d.pnl
    if (d.pnl > 1e-6) upDays++
    else if (d.pnl < -1e-6) downDays++
  }
  return { count, pnl: Math.round(pnl * 100) / 100, upDays, downDays }
})

const isCurrentMonth = computed(() => props.month === monthOf(props.today))

/**
 * 把「格子的日期」和「那天的汇总」合成一份**给模板用的视图**。
 *
 * 为什么不在模板里现查 `days.get(cell.day)`：那样每个格子要写四五次
 * 非空断言（`dayOf(...)!`），而 Vue 的模板表达式按 JS 解析，`!` 这种 TS 语法
 * 不一定编得过去；更重要的是「格子的颜色」和「格子里写什么」会散在模板各处，
 * 改一处忘一处。这里算完就只有一份。
 */
const cells = computed(() =>
  monthGrid(props.month, props.today).map((cell) => {
    const stats = props.days.get(cell.day) ?? null
    const ring = cell.isToday ? ' ring-2 ring-accent/40' : ''
    let cls = `cursor-default border-transparent bg-transparent text-transparent${ring}`
    if (cell.inMonth) {
      if (!stats) cls = `border-line/70 bg-elev/40 text-faint${ring}`
      else if (stats.pnl > 1e-6)
        cls = `border-up/30 bg-up-soft text-up hover:border-up hover:shadow-sm${ring}`
      else if (stats.pnl < -1e-6)
        cls = `border-down/30 bg-down-soft text-down hover:border-down hover:shadow-sm${ring}`
      else cls = `border-line bg-elev text-dim hover:border-accent${ring}`
    }
    return {
      key: cell.day,
      day: cell.day,
      date: cell.date,
      inMonth: cell.inMonth,
      isToday: cell.isToday,
      stats,
      cls,
      arrow: !stats ? '' : stats.pnl > 1e-6 ? '▲' : stats.pnl < -1e-6 ? '▼' : '—',
      title: !cell.inMonth
        ? ''
        : stats
          ? `${cell.day} · ${stats.count} 笔 · ${moneyShort(stats.pnl)}$ · 点开看当天每一笔`
          : `${cell.day} · 没有交易`,
    }
  }),
)

function move(delta: number): void {
  emit('update:month', shiftMonth(props.month, delta))
}
</script>

<template>
  <section class="min-w-0 rounded-xl border border-line bg-panel">
    <header class="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
      <div class="flex items-center gap-1">
        <button type="button" :class="navCls" title="上一个月" @click="move(-1)">‹</button>
        <h2 class="min-w-[7.5rem] text-center text-[15px] font-semibold text-ink">
          {{ monthLabel(month) }}
        </h2>
        <button type="button" :class="navCls" title="下一个月" @click="move(1)">›</button>
      </div>
      <button
        v-if="!isCurrentMonth"
        type="button"
        class="rounded border border-line px-2 py-0.5 text-[12px] text-dim transition hover:border-accent hover:text-accent"
        @click="emit('update:month', monthOf(today))"
      >
        回到本月
      </button>

      <div class="tnum ml-auto flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-dim">
        <span>{{ summary.count }} 笔</span>
        <span
          class="font-medium"
          :class="summary.pnl > 1e-6 ? 'text-up' : summary.pnl < -1e-6 ? 'text-down' : 'text-dim'"
        >
          {{ moneyShort(summary.pnl) }}$
        </span>
        <span class="text-up">{{ summary.upDays }} 天盈利</span>
        <span class="text-down">{{ summary.downDays }} 天亏损</span>
      </div>
    </header>

    <div class="grid grid-cols-7 gap-1 px-2 pt-2">
      <div
        v-for="(w, i) in WEEKDAYS"
        :key="w"
        class="pb-1 text-center text-[11px]"
        :class="i >= 5 ? 'text-faint/80' : 'text-faint'"
      >
        {{ w }}
      </div>
    </div>

    <div class="grid grid-cols-7 gap-1 p-2 pt-0">
      <button
        v-for="cell in cells"
        :key="cell.key"
        type="button"
        class="flex min-h-[92px] flex-col rounded-lg border p-1.5 text-left transition"
        :class="cell.cls"
        :disabled="!cell.inMonth || !cell.stats"
        :title="cell.title"
        @click="cell.stats && emit('select', cell.day)"
      >
        <div class="flex items-baseline gap-1">
          <span class="tnum text-[13px] font-medium">{{ cell.date }}</span>
          <span
            v-if="cell.isToday"
            class="rounded bg-accent px-1 text-[10px] leading-4 font-normal text-white"
          >
            今天
          </span>
          <span
            v-if="cell.stats && !cell.stats.closed"
            class="ml-auto rounded bg-sky-500/15 px-1 text-[10px] leading-4 text-sky-600"
            title="这天有一笔还没平仓"
          >
            未平
          </span>
        </div>

        <template v-if="cell.stats">
          <div class="tnum mt-auto flex items-baseline gap-1 text-[13px] font-semibold">
            <span>{{ cell.stats.count }}笔</span>
            <span class="text-[11px] font-normal opacity-50">/</span>
            <span>{{ moneyShort(cell.stats.pnl) }}$</span>
            <span class="ml-auto text-[10px] font-normal opacity-70">{{ cell.arrow }}</span>
          </div>
          <div class="tnum text-[10px] leading-4 opacity-70">
            {{ cell.stats.wins }} 胜 · {{ cell.stats.losses }} 负
          </div>
        </template>
      </button>
    </div>

    <footer
      class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-[11px] text-faint"
    >
      <span class="flex items-center gap-1.5">
        <span class="h-2.5 w-2.5 rounded-sm border border-up/30 bg-up-soft" />
        盈利日
      </span>
      <span class="flex items-center gap-1.5">
        <span class="h-2.5 w-2.5 rounded-sm border border-down/30 bg-down-soft" />
        亏损日
      </span>
      <span class="flex items-center gap-1.5">
        <span class="h-2.5 w-2.5 rounded-sm border border-line/70 bg-elev/40" />
        没有交易
      </span>
      <span class="ml-auto">点有交易的格子看当天每一笔</span>
    </footer>
  </section>
</template>
