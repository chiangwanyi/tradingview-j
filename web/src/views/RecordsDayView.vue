<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import RecordsCharts from '@/components/records/RecordsCharts.vue'
import RecordsKpi from '@/components/records/RecordsKpi.vue'
import RecordsToast from '@/components/records/RecordsToast.vue'
import RecordsTopBar from '@/components/records/RecordsTopBar.vue'
import SourceSwitch from '@/components/records/SourceSwitch.vue'
import TradeList from '@/components/records/TradeList.vue'
import { dayLabel, todayKey } from '@/lib/calendar'
import {
  aggregate,
  groupByDay,
  matchSource,
  moneyShort,
  recordDay,
  sourceOptions,
  tagUsage,
} from '@/lib/records'
import { sourceFilter } from '@/lib/records-ui'
import { useReplayStore } from '@/stores/replay'
import type { RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 【某个记录日】详情页（v0.21.0）。
 *
 * 从日历点进来，回答三个问题：**那天做了几笔、每一笔什么样、当时是怎么做的**。
 * 所以这一页是三段式 —— 统计（KPI）→ 图（累计曲线 / 每笔盈亏）→ 明细（点一行进回放），
 * 越往下越具体，最后一步正好交给画布。
 *
 * 几个刻意的选择：
 *
 *  1. **「那一天」= 记录生成日**（开仓那一刻落盘的时间），不是行情走到的那一天。
 *     今天补做 2025 年 1 月 / 5 月 / 8 月和 2026 年 6 月 / 9 月的行情，五笔都算今天。
 *     所以明细里必须带日期（`show-date`）：这五笔的行情日期是散的，不写日期就看不出
 *     哪一笔对应哪段行情。
 *  2. **日期在 URL 上**（`/records/day/2026-10-25`）。刷新、分享、浏览器返回都对得上，
 *     返回键直接回日历 —— 比「弹窗里看详情」自然得多。
 *  3. **上一天 / 下一天按「有记录的日子」跳**，不是自然日。复盘时想看的是
 *     「上一次练是什么时候」，不是「昨天（可能没做）」。只跳当前来源下还有记录的那些天。
 *  4. **数据只按日期 + 来源筛**，不做别的过滤：这一页要看的是「那天真实的全部」。
 */
const store = useReplayStore()
const route = useRoute()
const router = useRouter()

const today = todayKey()

const date = computed(() => String(route.params.date ?? ''))

const source = computed({
  get: () => sourceFilter.value,
  set: (v: string) => {
    sourceFilter.value = v
  },
})

const sourceRecs = computed(() => store.recordings.filter((r) => matchSource(r, source.value)))
const options = computed(() => sourceOptions(store.recordings))
const library = computed(() => tagUsage(store.recordings))

/** 当天的记录（记录日匹配，顺序由 aggregate 按记录时间排） */
const dayRecs = computed(() => sourceRecs.value.filter((r) => recordDay(r) === date.value))
const agg = computed(() => aggregate(dayRecs.value))

/**
 * 有记录的日子（当前来源下），用来做「上一个 / 下一个」。
 *
 * 排序键就是字符串日期：`YYYY-MM-DD` 的字典序和时间序一致，不必再解析成 Date。
 */
const tradeDays = computed(() => [...groupByDay(sourceRecs.value).keys()].sort())

const prevDay = computed(() => {
  const i = tradeDays.value.indexOf(date.value)
  if (i > 0) return tradeDays.value[i - 1]
  // 当前这一天没有记录时（比如刚把它删空），退到它之前最近的一个记录日
  const earlier = tradeDays.value.filter((d) => d < date.value)
  return earlier.length ? earlier[earlier.length - 1] : ''
})

const nextDay = computed(() => {
  const i = tradeDays.value.indexOf(date.value)
  if (i >= 0 && i < tradeDays.value.length - 1) return tradeDays.value[i + 1]
  return tradeDays.value.find((d) => d > date.value) ?? ''
})

function go(day: string): void {
  if (!day) return
  void router.push({ name: 'record-day', params: { date: day } })
}

function replay(rec: RecordingSummary): void {
  store.openRecordingAt(rec.id)
  void router.push({ name: 'replay' })
}

function remove(id: string): void {
  store.deleteRecording(id)
}

function applyTags(rec: RecordingSummary, tags: RecordTag[]): void {
  store.setRecordingTags(rec.id, tags)
}

/** 那一天的「最好 / 最差」写得具体一点：金额 + 那一笔的**行情**时间（可能压根不是同一天） */
const bestText = computed(() => {
  const b = agg.value.best
  if (!b) return '—'
  return `${moneyShort(b.rec.stats.realizedPnl)}$ · 行情 ${b.time}`
})
const worstText = computed(() => {
  const w = agg.value.worst
  if (!w) return '—'
  return `${moneyShort(w.rec.stats.realizedPnl)}$ · 行情 ${w.time}`
})

onMounted(() => {
  store.connect()
  store.listRecordings()
})

// 日期不合法（手改 URL、老链接）就回日历，别停在一张空白页上
watch(
  date,
  (d) => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(d)) void router.replace({ name: 'records' })
  },
  { immediate: true },
)
</script>

<template>
  <div class="min-h-screen bg-surface text-ink">
    <RecordsTopBar :title="dayLabel(date)" :back-to="{ name: 'records' }" back-text="记录日历">
      <template #sub>
        <div class="mx-auto flex w-full max-w-[1440px] flex-wrap items-center gap-2 px-4 pb-2">
          <div class="flex items-center gap-1">
            <button
              type="button"
              class="rounded border border-line px-2 py-0.5 text-[12px] text-dim transition hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="!prevDay"
              :title="prevDay ? `上一个有记录的日子：${prevDay}` : '前面没有别的记录日了'"
              @click="go(prevDay)"
            >
              ‹ 上一个记录日
            </button>
            <button
              type="button"
              class="rounded border border-line px-2 py-0.5 text-[12px] text-dim transition hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="!nextDay"
              :title="nextDay ? `下一个有记录的日子：${nextDay}` : '后面没有别的记录日了'"
              @click="go(nextDay)"
            >
              下一个记录日 ›
            </button>
            <button
              v-if="date !== today"
              type="button"
              class="rounded px-1.5 py-0.5 text-[12px] text-faint transition hover:text-accent"
              title="回到日历上的今天"
              @click="go(today)"
            >
              今天
            </button>
          </div>

          <span class="text-[12px] text-faint">来源</span>
          <SourceSwitch v-model="source" :options="options" />

          <span class="tnum ml-auto text-[12px] text-faint">
            当天 {{ agg.count }} 笔 · {{ moneyShort(agg.totalPnl) }}$
          </span>
        </div>
      </template>
    </RecordsTopBar>

    <main class="mx-auto flex w-full max-w-[1440px] flex-col gap-3 px-4 py-3">
      <!-- 这一天没有记录 -->
      <section
        v-if="!store.recordings.length"
        class="rounded-lg border border-line bg-panel px-5 py-8 text-[14px] leading-6 text-dim"
      >
        <h2 class="mb-1 text-[15px] font-semibold text-ink">还没有任何交易记录</h2>
        <p>开仓自动开始记、平仓自动收尾，做一笔回放交易再回来。</p>
        <RouterLink :to="{ name: 'replay' }" class="mt-3 inline-block text-accent hover:underline">
          ← 去复盘做一笔
        </RouterLink>
      </section>

      <section
        v-else-if="agg.count === 0"
        class="rounded-lg border border-line bg-panel px-5 py-10 text-center text-[14px] text-dim"
      >
        <p class="text-[15px] font-medium text-ink">{{ dayLabel(date) }} 没有记录</p>
        <p class="mt-1 text-[13px] text-faint">
          当前来源下这一天没做过交易；换一个来源，或者翻到别的记录日看看。
        </p>
        <div class="mt-4 flex items-center justify-center gap-2">
          <button
            type="button"
            class="rounded border border-line px-2.5 py-1 text-[13px] text-dim hover:border-accent hover:text-accent disabled:opacity-40"
            :disabled="!prevDay"
            @click="go(prevDay)"
          >
            ‹ 上一个记录日
          </button>
          <RouterLink
            :to="{ name: 'records' }"
            class="rounded border border-line px-2.5 py-1 text-[13px] text-dim hover:border-accent hover:text-accent"
          >
            回到日历
          </RouterLink>
          <button
            type="button"
            class="rounded border border-line px-2.5 py-1 text-[13px] text-dim hover:border-accent hover:text-accent disabled:opacity-40"
            :disabled="!nextDay"
            @click="go(nextDay)"
          >
            下一个记录日 ›
          </button>
        </div>
      </section>

      <template v-else>
        <!-- ---------------- 当日统计 ---------------- -->
        <RecordsKpi :agg="agg" scope="（当天）" />

        <!-- 极值单加一句「哪一段行情」：只看金额不知道是哪一笔（这一天里的几笔可能来自好几年） -->
        <section
          class="flex flex-wrap items-center gap-x-5 gap-y-1 rounded-lg border border-line bg-panel px-3 py-2 text-[12px] text-faint"
        >
          <span>
            最好的一笔
            <b class="tnum ml-1 text-up">{{ bestText }}</b>
          </span>
          <span>
            最差的一笔
            <b class="tnum ml-1 text-down">{{ worstText }}</b>
          </span>
          <span class="tnum">共 {{ agg.totalSteps }} 步操作</span>
          <button
            type="button"
            class="ml-auto text-accent hover:underline"
            @click="store.listRecordings()"
          >
            重新拉一次列表
          </button>
        </section>

        <!-- ---------------- 图表 ---------------- -->
        <RecordsCharts :agg="agg" />

        <!-- ---------------- 明细 ---------------- -->
        <section class="rounded-lg border border-line bg-panel">
          <header class="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
            <h2 class="text-[13px] font-medium text-ink">当天每一笔</h2>
            <span class="text-[11px] text-faint">
              {{ agg.count }} 笔 · 点任意一行回到画布逐格看这一笔怎么进的场
            </span>
            <span class="ml-auto text-[11px] text-faint">按记录时间正序</span>
          </header>
          <TradeList
            :rows="agg.rows"
            :library="library"
            empty-text="这一天没有记录"
            @replay="replay"
            @remove="remove"
            @tags="applyTags"
          />
        </section>
      </template>
    </main>

    <RecordsToast />
  </div>
</template>
