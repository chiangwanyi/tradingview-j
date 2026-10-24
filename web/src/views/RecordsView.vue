<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import RecordCalendar from '@/components/records/RecordCalendar.vue'
import RecordsStatsPanel from '@/components/records/RecordsStatsPanel.vue'
import RecordsToast from '@/components/records/RecordsToast.vue'
import RecordsTopBar from '@/components/records/RecordsTopBar.vue'
import SourceSwitch from '@/components/records/SourceSwitch.vue'
import { monthLabel, monthOf, todayKey } from '@/lib/calendar'
import {
  groupByDay,
  matchSource,
  moneyShort,
  pnlSign,
  sourceKeyOf,
  sourceNameOf,
  sourceOptions,
  tagUsage,
} from '@/lib/records'
import { sourceFilter, tagFilter } from '@/lib/records-ui'
import { useReplayStore } from '@/stores/replay'
import type { RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 【交易记录】首页（v0.21.0 重构）：**月历 + 统计**两个视图，共用一条来源筛选。
 *
 * 为什么首页是日历而不是统计：这一页的使用场景是「每天练完复盘、回头看一眼」。
 * 打开时最想确认的是「我哪天练了、那天赚没赚」，而不是十二个总指标 ——
 * 统计是「过一段时间回头看」的动作，所以它在第二个 tab，一按就到。
 *
 * 三个必须守住的边界：
 *
 *  1. **一切从筛选后的这一份记录算**。日历、统计、明细都读 `sourceRecs`，
 *     所以切来源时三处永远一致；不存在「日历上是全天、点开只有机器人」。
 *  2. **这一页不算钱**。每笔的盈亏来自后端 stats（见 lib/records.ts），
 *     这里只按天求和。改日历长什么样都不会让数字对不上。
 *  3. **点某一天 = 换一页**（`/records/day/:date`），不是弹窗。理由很实际：
 *     详情页要看图、要打标签、要点回放，弹窗里做这些既挤又容易误关；
 *     而且换页之后浏览器的返回键正好回到日历，符合直觉。
 */

const store = useReplayStore()
const route = useRoute()
const router = useRouter()

const today = todayKey()

/** 当前是「日历」还是「统计」。放 URL 上：刷新、前进后退都还在这一页 */
const view = computed<'calendar' | 'stats'>(() =>
  route.query.view === 'stats' ? 'stats' : 'calendar',
)

function setView(v: 'calendar' | 'stats'): void {
  const q = { ...route.query }
  if (v === 'stats') q.view = 'stats'
  else delete q.view
  void router.replace({ query: q })
}

/**
 * 两个界面偏好包一层可写 computed。
 *
 * 为什么不直接 `v-model="sourceFilter"`：那是从模块里 import 进来的 ref，
 * 模板里的赋值要穿过 setup 代理才写得到 `.value`，编译器版本一变就可能变成
 * 「悄悄不生效」。包一层之后读写都在脚本里，没有这层不确定性。
 */
const source = computed({
  get: () => sourceFilter.value,
  set: (v: string) => {
    sourceFilter.value = v
  },
})

const selectedTags = computed({
  get: () => tagFilter.value,
  set: (v: string[]) => {
    tagFilter.value = v
  },
})

// ---------------- 数据 ----------------

/** 按来源筛过的记录：日历和统计都只认这一份 */
const sourceRecs = computed(() => store.recordings.filter((r) => matchSource(r, source.value)))
const days = computed(() => groupByDay(sourceRecs.value))
/** 来源开关上的数字：**全部记录**的分布，不随当前筛选变 —— 否则选了「手动」之后「机器人 4」会变成 0 */
const options = computed(() => sourceOptions(store.recordings))
/** 标签的全局用量（打标签面板里的「已有标签」） */
const library = computed(() => tagUsage(store.recordings))

// ---------------- 月份 ----------------

/**
 * 打开时看哪一个月。
 *
 * 优先「今天所在的月」—— 用户说「进入首页即见本月日历」。但行情时间来自回放数据，
 * 可能根本不是今天：这时候停在一个空月份上，页面看起来就像坏了。所以退一步去看
 * **最近有交易的那个月**。两者都没有才回到本月，让用户自己按月翻。
 */
function pickMonth(): string {
  const current = monthOf(today)
  const keys = [...days.value.keys()]
  if (keys.some((d) => monthOf(d) === current)) return current
  const latest = keys.sort().at(-1)
  return latest ? monthOf(latest) : current
}

const month = ref(pickMonth())
/** 用户手动翻过月就不再自动跳（否则切一下来源，视线刚对准的月份就被拽走） */
const monthPinned = ref(false)

watch(
  () => [store.recordings.length, source.value] as const,
  () => {
    if (!monthPinned.value) month.value = pickMonth()
  },
)

// 换来源 = 换一个语境：允许重新自动选月份
watch(source, () => {
  monthPinned.value = false
})

function setMonth(m: string): void {
  monthPinned.value = true
  month.value = m
}

/** 本月里「有交易的那些天」 */
const monthDays = computed(() =>
  [...days.value.values()].filter((d) => monthOf(d.day) === month.value),
)

const monthSummary = computed(() => {
  let count = 0
  let pnl = 0
  let up = 0
  let down = 0
  let best: { day: string; pnl: number } | null = null
  let worst: { day: string; pnl: number } | null = null
  for (const d of monthDays.value) {
    count += d.count
    pnl += d.pnl
    if (d.pnl > 1e-6) up++
    else if (d.pnl < -1e-6) down++
    if (!best || d.pnl > best.pnl) best = { day: d.day, pnl: d.pnl }
    if (!worst || d.pnl < worst.pnl) worst = { day: d.day, pnl: d.pnl }
  }
  return {
    count,
    pnl: Math.round(pnl * 100) / 100,
    up,
    down,
    days: monthDays.value.length,
    avg: monthDays.value.length ? Math.round((pnl / monthDays.value.length) * 100) / 100 : 0,
    best,
    worst,
  }
})

/**
 * 是不是「自动跳到了一个不是本月的月份」。
 *
 * 这句话必须有：用户打开记录页看到的是 5 月，第一反应是「我的 10 月呢」。
 * 与其让他自己去翻，不如直接说清楚「10 月没有交易，这是最近有记录的月份」。
 */
const autoJumped = computed(
  () => !monthPinned.value && month.value !== monthOf(today) && monthSummary.value.count > 0,
)

/** 本月的来源构成（手动 / 各策略），用于首页右侧那块 */
const monthSources = computed(() => {
  const m = new Map<string, { label: string; count: number; pnl: number; bot: boolean }>()
  for (const d of monthDays.value) {
    for (const r of d.rows) {
      const key = sourceKeyOf(r)
      const cur = m.get(key) ?? { label: sourceNameOf(r), count: 0, pnl: 0, bot: sourceKeyOf(r) !== 'human' }
      cur.count++
      cur.pnl = Math.round((cur.pnl + r.stats.realizedPnl) * 100) / 100
      m.set(key, cur)
    }
  }
  return [...m.entries()]
    .map(([key, v]) => ({ key, ...v }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
})

/** 本月有多少笔是机器人做的（顶部那句话用） */
const botShare = computed(() => {
  const total = monthSummary.value.count
  const bot = monthSources.value.filter((s) => s.bot).reduce((n, s) => n + s.count, 0)
  return { total, bot, pct: total ? Math.round((bot / total) * 100) : 0 }
})

// ---------------- 动作 ----------------

function openDay(day: string): void {
  void router.push({ name: 'record-day', params: { date: day } })
}

/** 回放：把这条记录记进 store，跳回复盘页由画布接住（见 stores/replay.ts 的 pendingPlaybackId）。 */
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

function resetSource(): void {
  source.value = ''
}

onMounted(() => {
  // 直接打开 /records（刷新页面、从书签进来）也要能用：自己连一次、自己拉一次列表
  store.connect()
  store.listRecordings()
})

function tone(n: number): string {
  if (n > 1e-6) return 'text-up'
  if (n < -1e-6) return 'text-down'
  return 'text-dim'
}

const tabBtn = 'rounded px-2.5 py-1 text-[13px] transition'
</script>

<template>
  <div class="min-h-screen bg-surface text-ink">
    <RecordsTopBar title="交易记录" :back-to="{ name: 'replay' }" back-text="复盘">
      <div class="flex items-center gap-0.5 rounded-md border border-line bg-elev/60 p-0.5">
        <button
          type="button"
          :class="[tabBtn, view === 'calendar' ? 'bg-ink text-white shadow-sm' : 'text-dim hover:text-ink']"
          @click="setView('calendar')"
        >
          日历
        </button>
        <button
          type="button"
          :class="[tabBtn, view === 'stats' ? 'bg-ink text-white shadow-sm' : 'text-dim hover:text-ink']"
          @click="setView('stats')"
        >
          统计
        </button>
      </div>

      <template #sub>
        <div
          class="mx-auto flex w-full max-w-[1440px] flex-wrap items-center gap-2 px-4 pb-2"
        >
          <span class="text-[12px] text-faint">来源</span>
          <SourceSwitch v-model="source" :options="options" />
          <span class="tnum ml-auto text-[12px] text-faint">
            {{ sourceRecs.length }} / {{ store.recordings.length }} 笔
          </span>
        </div>
      </template>
    </RecordsTopBar>

    <main class="mx-auto flex w-full max-w-[1440px] flex-col gap-3 px-4 py-3">
      <!-- ---------------- 一笔记录都没有：讲清楚这些记录是怎么来的 ---------------- -->
      <section
        v-if="store.recordings.length === 0"
        class="rounded-lg border border-line bg-panel px-5 py-8 text-[14px] leading-7 text-dim"
      >
        <h2 class="mb-1 text-[15px] font-semibold text-ink">还没有交易记录</h2>
        <p>
          <b class="text-ink">开仓的那一刻自动开始记，仓位平掉的那一刻自动收尾</b>
          —— 不需要点任何开关，也不会有「正在录制」的提示。记下的是这一笔从进场到出场的
          全部操作（【下一根】、画图、挂单、语音），回放时一格一格演给你看。
        </p>
        <p class="mt-2 text-[13px] text-faint">
          记录落在 <span class="tnum">{{ store.recordingDir || '（未配置记录目录）' }}</span
          >； 做几笔模拟交易再回来，这一页会自动把它排进日历。
        </p>
        <RouterLink
          :to="{ name: 'replay' }"
          class="mt-4 inline-block text-[14px] text-accent hover:underline"
        >
          ← 去复盘做一笔
        </RouterLink>
      </section>

      <template v-else>
        <!-- ---------------- 日历视图 ---------------- -->
        <div v-if="view === 'calendar'" class="grid grid-cols-1 gap-3 xl:grid-cols-[minmax(0,1fr)_300px]">
          <div class="flex min-w-0 flex-col gap-3">
            <div
              v-if="sourceRecs.length === 0"
              class="rounded-lg border border-line bg-panel px-4 py-3 text-[13px] text-dim"
            >
              当前来源下没有记录。
              <button type="button" class="text-accent hover:underline" @click="resetSource">
                看全部来源
              </button>
            </div>

            <p
              v-if="autoJumped"
              class="rounded-lg border border-line bg-accent-soft px-3 py-2 text-[12px] text-dim"
            >
              {{ monthLabel(monthOf(today)) }}还没有交易，已跳到最近有记录的
              <b class="text-ink">{{ monthLabel(month) }}</b>。
              <button
                type="button"
                class="ml-1 text-accent hover:underline"
                @click="setMonth(monthOf(today))"
              >
                去看本月日历
              </button>
            </p>

            <RecordCalendar
              :month="month"
              :days="days"
              :today="today"
              @update:month="setMonth"
              @select="openDay"
            />

            <p class="px-1 text-[12px] leading-5 text-faint">
              日历按<b class="text-dim">交易日</b>排（K 线时间），不是按记录生成时间 ——
              同一天补做前几天的行情，格子落在行情那一天。点有交易的格子进当天详情。
            </p>
          </div>

          <!-- 本月概览：右侧只放「一眼能读完」的几行，图表留给统计页 -->
          <aside class="flex flex-col gap-3">
            <section class="rounded-xl border border-line bg-panel p-3">
              <header class="flex items-baseline gap-2">
                <h2 class="text-[13px] font-medium text-ink">{{ monthLabel(month) }}概览</h2>
                <span class="ml-auto text-[11px] text-faint">{{ monthSummary.days }} 天有交易</span>
              </header>
              <dl class="mt-2 space-y-1.5 text-[12px]">
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">交易笔数</dt>
                  <dd class="tnum text-ink">{{ monthSummary.count }} 笔</dd>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">累计盈亏</dt>
                  <dd class="tnum font-semibold" :class="tone(monthSummary.pnl)">
                    {{ moneyShort(monthSummary.pnl) }}$
                  </dd>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">盈利 / 亏损天数</dt>
                  <dd class="tnum">
                    <span class="text-up">{{ monthSummary.up }}</span>
                    <span class="mx-1 text-faint">/</span>
                    <span class="text-down">{{ monthSummary.down }}</span>
                  </dd>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">日均</dt>
                  <dd class="tnum" :class="tone(monthSummary.avg)">
                    {{ moneyShort(monthSummary.avg) }}$
                  </dd>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">最好的一天</dt>
                  <dd class="tnum text-up">
                    <template v-if="monthSummary.best && pnlSign(monthSummary.best.pnl) > 0">
                      {{ monthSummary.best.day.slice(5) }} · {{ moneyShort(monthSummary.best.pnl) }}$
                    </template>
                    <template v-else>—</template>
                  </dd>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="text-faint">最差的一天</dt>
                  <dd class="tnum text-down">
                    <template v-if="monthSummary.worst && pnlSign(monthSummary.worst.pnl) < 0">
                      {{ monthSummary.worst.day.slice(5) }} · {{ moneyShort(monthSummary.worst.pnl) }}$
                    </template>
                    <template v-else>—</template>
                  </dd>
                </div>
              </dl>
            </section>

            <section v-if="monthSources.length" class="rounded-xl border border-line bg-panel p-3">
              <header class="flex items-baseline gap-2">
                <h2 class="text-[13px] font-medium text-ink">本月是谁在做</h2>
                <span v-if="botShare.bot" class="ml-auto text-[11px] text-faint">
                  机器人占 {{ botShare.pct }}%
                </span>
              </header>
              <ul class="mt-2 space-y-1.5 text-[12px]">
                <li
                  v-for="s in monthSources"
                  :key="s.key"
                  class="flex items-baseline justify-between gap-2"
                >
                  <span class="flex min-w-0 items-baseline gap-1.5">
                    <span
                      class="h-1.5 w-1.5 shrink-0 rounded-full"
                      :class="s.bot ? 'bg-violet-500' : 'bg-faint'"
                    />
                    <span class="truncate text-dim">{{ s.label }}</span>
                    <span class="tnum shrink-0 text-faint">{{ s.count }} 笔</span>
                  </span>
                  <span class="tnum shrink-0" :class="tone(s.pnl)">{{ moneyShort(s.pnl) }}$</span>
                </li>
              </ul>
              <p class="mt-2 text-[11px] leading-4 text-faint">
                来源在开仓那一刻定下；机器人按跑的<b class="text-dim">策略</b>细分。
              </p>
            </section>
          </aside>
        </div>

        <!-- ---------------- 统计视图 ---------------- -->
        <RecordsStatsPanel
          v-else
          v-model:selected-tags="selectedTags"
          :records="sourceRecs"
          :library="library"
          @replay="replay"
          @remove="remove"
          @tags="applyTags"
        />
      </template>
    </main>

    <RecordsToast />
  </div>
</template>
