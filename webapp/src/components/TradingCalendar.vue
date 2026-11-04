<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { WEEKDAYS, monthGrid, monthLabel, monthOf, shiftMonth, todayKey } from '@/lib/calendar'
import type { DayPnl, DayTrade } from '@/data/accounts'

/**
 * 交易日历（月视图）：这台仪表回答的是「这个账户哪几天赚了、哪几天亏了」。
 *
 * ## 格子里的「天」是**交易日**，不是日历日
 *
 * 归日是后端的事（`PropFirmClock`：交易日 D = `[D 06:00, D+1 04:45)`），
 * 组件只按 `DayPnl.day` 落格子。所以凌晨 03:10 平的那笔会出现在**前一天**的格子里 ——
 * 这不是画错了，是口径。别在这里拿 `entryBarTime` 自己切前 10 个字符。
 *
 * ## 颜色
 *
 * 盈利绿、亏损红（和 K 线、成交流水、交易记录页同一套）。只靠颜色不够：
 * 格子里同时写金额的正负号，色觉障碍和灰度打印都读得出来。
 *
 * ## 数据只拉一次
 *
 * 父页面把**全部历史**一次拿齐（`GET /api/pf-accounts/{id}/calendar`），
 * 翻月只是换个筛选条件，不发请求 —— 图省那点数据量去按月请求，
 * 换来的只会是「切月时日历先空一下」。
 */
const props = defineProps<{
  /** 后端给的按天汇总，升序 */
  days: DayPnl[]
  /** 加载中：别把「还没拉到」画成「这天没交易」 */
  loading?: boolean
}>()

const today = todayKey()

/** 默认停在**最后一笔交易**那个月：考核号常常是几个月前建的，停在今天会看到一片空。 */
const latestMonth = computed(() =>
  props.days.length ? monthOf(props.days[props.days.length - 1].day) : monthOf(today),
)

const month = ref(latestMonth.value)

// 数据是异步来的：第一次拿到（或换了账户）就跳到有数据的那个月
watch(latestMonth, (m) => {
  month.value = m
})

const byDay = computed(() => {
  const m = new Map<string, DayPnl>()
  for (const d of props.days) m.set(d.day, d)
  return m
})

/** 本月汇总（只算属于这个月的格子）。 */
const summary = computed(() => {
  let trades = 0
  let net = 0
  let days = 0
  for (const d of props.days) {
    if (monthOf(d.day) !== month.value) continue
    days++
    trades += d.trades
    net += d.netPnl
  }
  return { trades, net: Math.round(net * 100) / 100, days }
})

/**
 * 格子的视图数据。
 *
 * 不在模板里现查 `byDay.get(cell.day)`：那样每个格子要写四五次非空判断，
 * 而且「颜色」和「显示什么」会散在模板各处，改一处忘一处。
 */
const cells = computed(() =>
  monthGrid(month.value, today).map((cell) => {
    const stats = byDay.value.get(cell.day) ?? null
    const ring = cell.isToday ? ' ring-2 ring-accent/30' : ''
    let cls = `border-transparent bg-transparent text-transparent${ring}`
    if (cell.inMonth) {
      if (props.loading) cls = `border-line/70 bg-elev/30 text-faint${ring}`
      else if (!stats) cls = `border-line/70 bg-elev/40 text-faint${ring}`
      else if (stats.netPnl > 0.005)
        cls = `border-up/30 bg-up-soft text-up hover:border-up${ring}`
      else if (stats.netPnl < -0.005)
        cls = `border-down/30 bg-down-soft text-down hover:border-down${ring}`
      else cls = `border-line bg-elev text-dim hover:border-line-strong${ring}`
    }
    return {
      key: cell.day,
      day: cell.day,
      date: cell.date,
      inMonth: cell.inMonth,
      isToday: cell.isToday,
      stats,
      cls,
      title: !cell.inMonth
        ? ''
        : stats
          ? `${cell.day} · ${stats.trades} 笔 · ${signed(stats.netPnl)}，点开看当天每一笔`
          : `${cell.day} · 没有交易`,
    }
  }),
)

const selected = ref<string | null>(null)

/** 换月就把展开的那天收起来：留着一个在本月看不到的日子，只会让人以为没反应。 */
watch(month, () => {
  selected.value = null
})

const openDay = computed<DayPnl | null>(() =>
  selected.value ? (byDay.value.get(selected.value) ?? null) : null,
)

function toggle(day: string, has: boolean): void {
  if (!has) return
  selected.value = selected.value === day ? null : day
}

function move(delta: number): void {
  month.value = shiftMonth(month.value, delta)
}

/** `+$52.50` / `-$40.00` / `$0.00`。金额一律带符号，颜色只是辅助。 */
function signed(n: number): string {
  const abs = Math.abs(n)
  const text = `$${abs.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  return n > 0.005 ? `+${text}` : n < -0.005 ? `-${text}` : text
}

/** 一笔的「多/空」：`side` 是后端的小写协议串（buy / sell）。 */
function sideLabel(t: DayTrade): string {
  if (t.side === 'buy') return '多'
  if (t.side === 'sell') return '空'
  return '—'
}

/** `2026-10-29 09:05` → `09:05`（同一行里已经是这一天的了，年月日重复没意义）。 */
function hhmm(time: string | null): string {
  return time && time.length >= 16 ? time.slice(11, 16) : '—'
}

const navCls =
  'flex h-7 w-7 items-center justify-center rounded-md border border-line text-[15px] leading-none text-dim transition-colors hover:border-line-strong hover:text-ink'
</script>

<template>
  <section class="min-w-0 rounded-xl border border-line bg-panel">
    <header class="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-5 py-3">
      <h2 class="text-[13px] font-medium text-ink">交易日历</h2>

      <div class="ml-auto flex items-center gap-1.5">
        <button type="button" :class="navCls" title="上一个月" @click="move(-1)">‹</button>
        <span class="tnum min-w-[6.5rem] text-center text-[13px] font-medium text-ink">
          {{ monthLabel(month) }}
        </span>
        <button type="button" :class="navCls" title="下一个月" @click="move(1)">›</button>
        <button
          v-if="month !== latestMonth"
          type="button"
          class="ml-1 rounded-md border border-line px-2 py-1 text-[11px] text-dim transition-colors hover:border-line-strong hover:text-ink"
          @click="month = latestMonth"
        >
          最近有交易
        </button>
      </div>

      <div class="tnum flex w-full flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-faint sm:w-auto">
        <span>{{ summary.days }} 个交易日</span>
        <span>{{ summary.trades }} 笔</span>
        <span
          class="font-medium"
          :class="summary.net > 0.005 ? 'text-up' : summary.net < -0.005 ? 'text-down' : 'text-dim'"
        >
          {{ signed(summary.net) }}
        </span>
      </div>
    </header>

    <div class="grid grid-cols-7 gap-1 px-3 pt-3">
      <div
        v-for="(w, i) in WEEKDAYS"
        :key="w"
        class="pb-1 text-center text-[11px]"
        :class="i >= 5 ? 'text-faint/70' : 'text-faint'"
      >
        {{ w }}
      </div>
    </div>

    <div class="grid grid-cols-7 gap-1 p-3 pt-0">
      <button
        v-for="cell in cells"
        :key="cell.key"
        type="button"
        class="flex min-h-[74px] flex-col rounded-lg border p-2 text-left transition-colors"
        :class="[cell.cls, cell.stats && cell.inMonth ? 'cursor-pointer' : 'cursor-default']"
        :disabled="!cell.inMonth || !cell.stats"
        :title="cell.title"
        @click="toggle(cell.day, !!cell.stats)"
      >
        <div class="flex items-baseline gap-1">
          <span class="tnum text-[12px] font-medium">{{ cell.date }}</span>
          <span
            v-if="cell.isToday"
            class="rounded bg-accent px-1 text-[10px] leading-4 font-normal text-white"
          >
            今天
          </span>
        </div>

        <template v-if="cell.stats">
          <span class="tnum mt-auto text-[13px] leading-5 font-semibold">
            {{ signed(cell.stats.netPnl) }}
          </span>
          <span class="tnum text-[10px] leading-4 opacity-70">{{ cell.stats.trades }} 笔</span>
        </template>
      </button>
    </div>

    <!-- 点开的那一天：就地展开当天每一笔，不跳页 -->
    <div v-if="openDay" class="border-t border-line px-5 py-3">
      <div class="flex items-baseline justify-between gap-3">
        <p class="text-[12px] font-medium text-ink">
          {{ openDay.day }} 的每一笔
          <span class="ml-1 font-normal text-faint">
            {{ openDay.trades }} 笔 · {{ openDay.wins }} 胜 {{ openDay.losses }} 负 ·
            毛利 {{ signed(openDay.grossPnl) }} · 手续费 {{ signed(-openDay.commission) }}
          </span>
        </p>
        <button
          type="button"
          class="text-[11px] text-faint transition-colors hover:text-ink"
          @click="selected = null"
        >
          收起
        </button>
      </div>

      <table class="mt-2 w-full text-[12px]">
        <thead>
          <tr class="text-left text-[10px] tracking-wider text-faint uppercase">
            <th class="py-1 pr-4 font-normal">开仓</th>
            <th class="py-1 pr-4 font-normal">平仓</th>
            <th class="py-1 pr-4 font-normal">方向</th>
            <th class="py-1 pr-4 text-right font-normal">手数</th>
            <th class="py-1 text-right font-normal">净额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in openDay.items" :key="t.id" class="border-t border-line">
            <td class="tnum py-1 pr-4 text-ink">{{ hhmm(t.entryBarTime) }}</td>
            <td class="tnum py-1 pr-4 text-dim">{{ hhmm(t.exitBarTime) }}</td>
            <td class="py-1 pr-4" :class="t.side === 'sell' ? 'text-down' : 'text-up'">
              {{ sideLabel(t) }}
            </td>
            <td class="tnum py-1 pr-4 text-right text-dim">{{ t.quantity ?? '—' }}</td>
            <td
              class="tnum py-1 text-right font-medium"
              :class="
                t.netPnl == null ? 'text-faint' : t.netPnl > 0.005 ? 'text-up' : t.netPnl < -0.005 ? 'text-down' : 'text-dim'
              "
            >
              {{ t.netPnl == null ? '—' : signed(t.netPnl) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <footer class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-5 py-2 text-[11px] text-faint">
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
      <span class="ml-auto">一个格子 = 一个交易日（06:00 → 次日 04:45）</span>
    </footer>
  </section>
</template>
