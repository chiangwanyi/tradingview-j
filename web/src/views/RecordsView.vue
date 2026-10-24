<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import RecordsCharts from '@/components/records/RecordsCharts.vue'
import {
  aggregate,
  dirText,
  EMPTY_FILTER,
  filterRecords,
  moneyText,
  pctText,
  pnlText,
  sizeText,
  sortRows,
  symbolOptions,
} from '@/lib/records'
import type { RecordsFilter, SortKey } from '@/lib/records'
import { useReplayStore } from '@/stores/replay'
import type { RecordingSummary } from '@/types/protocol'

/**
 * 【交易记录】独立页：统计 + 明细。
 *
 * 为什么从底部 Tab 搬出来：交易记录本来就不是「一边看盘一边扫一眼」的东西。
 * 停在底部 180px 的抽屉里，能看到的只有一行摘要；而复盘真正想回答的问题是
 * 「我这一段时间到底赚没赚、靠什么赚、哪一类单子在亏」—— 那是要占满一屏、
 * 要能对着图慢慢看的。所以：**看盘页只留回放入口，统计和明细搬到这一页**。
 *
 * 两个必须守住的边界：
 *
 *  1. **这一页不算钱**。每一笔的盈亏、MFE/MAE 都来自后端 stats；这里只做
 *     求和、排序、分箱（见 lib/records.ts 的说明）。所以无论怎么改这里的图，
 *     都不会出现「页面上的钱和记录里的钱对不上」。
 *  2. **点【回放】要回到画布**。回放依赖 K 线画布，那是 `/` 上的东西，
 *     所以这里只负责「把要放的那条记进 store，再跳回去」，由复盘页接住。
 *
 * 列表的默认顺序是**交易时间正序**（和累计曲线一致）；点表头可以按
 * 盈亏 / 持仓根数重排 —— 那两种顺序只在「找极端的那几笔」时有用，
 * 所以不改变上面的统计（统计永远按时间轴算）。
 */

const store = useReplayStore()
const router = useRouter()

const filter = reactive<RecordsFilter>({ ...EMPTY_FILTER })
const sortKey = ref<SortKey>('time')
const sortDir = ref<1 | -1>(1)
/** 删除的二次确认：记录删了没有回收站，第一下只是「上膛」 */
const armed = ref('')
let armTimer = 0

const all = computed(() => store.recordings)
const filtered = computed(() => filterRecords(all.value, filter))
const agg = computed(() => aggregate(filtered.value))
const symbols = computed(() => symbolOptions(all.value))
const rows = computed(() => sortRows(agg.value.rows, sortKey.value, sortDir.value))
const hasFilter = computed(() =>
  (Object.keys(EMPTY_FILTER) as (keyof RecordsFilter)[]).some((k) => filter[k] !== EMPTY_FILTER[k]),
)

const totalSize = computed(() => all.value.reduce((n, r) => n + r.size, 0))

// ---------------- 展示辅助 ----------------

/** 赚红亏绿（国内习惯），和 K 线、列表、图表用的是同一套语义色。 */
function tone(n: number): string {
  if (n > 1e-6) return 'text-red-600 dark:text-red-400'
  if (n < -1e-6) return 'text-emerald-600 dark:text-emerald-400'
  return 'text-ink'
}

function exitText(rec: RecordingSummary): string {
  const exit = rec.stats.exitTime || rec.endTime
  if (!exit) return '—'
  const entry = rec.stats.entryTime
  // 同一天就只留时分，省下的宽度够把「MFE/MAE」写清楚
  if (entry && exit.startsWith(entry.slice(0, 10))) return exit.slice(11)
  return exit
}

function clock(at: number): string {
  if (!at) return ''
  const d = new Date(at * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes(),
  )}`
}

function sortBy(key: SortKey): void {
  if (sortKey.value === key) sortDir.value = sortDir.value === 1 ? -1 : 1
  else {
    sortKey.value = key
    sortDir.value = key === 'time' ? 1 : -1 // 换列时：时间从早到晚，金额/根数从大到小
  }
}

function arrow(key: SortKey): string {
  if (sortKey.value !== key) return ''
  return sortDir.value === 1 ? '↑' : '↓'
}

function resetFilter(): void {
  Object.assign(filter, EMPTY_FILTER)
}

// ---------------- 动作 ----------------

/** 回放：把这条记录记进 store，跳回复盘页由画布接住（见 stores/replay.ts 的 pendingPlaybackId）。 */
function replay(rec: RecordingSummary): void {
  store.openRecordingAt(rec.id)
  void router.push({ name: 'replay' })
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
  store.deleteRecording(rec.id)
}

const statusText = computed(() => {
  switch (store.status) {
    case 'open':
      return '已连接'
    case 'connecting':
      return '连接中…'
    default:
      return '已断开（正在重连）'
  }
})
const statusColor = computed(() => {
  switch (store.status) {
    case 'open':
      return 'bg-emerald-500'
    case 'connecting':
      return 'bg-amber-400'
    default:
      return 'bg-red-500'
  }
})
const themeLabel = computed(() => (store.theme === 'dark' ? '浅色' : '深色'))

onMounted(() => {
  // 直接打开 /records（刷新页面、从书签进来）也要能用：自己连一次、自己拉一次列表
  store.connect()
  store.listRecordings()
})

const btn =
  'rounded border border-line px-2 py-1 text-[12px] text-dim transition hover:border-accent hover:text-accent'
const field =
  'rounded border border-line bg-elev px-1.5 py-1 text-[12px] text-ink outline-none focus:border-accent'
const th = 'whitespace-nowrap px-2 py-1.5 text-left font-medium text-faint'
const td = 'whitespace-nowrap px-2 py-1.5'
</script>

<template>
  <div class="min-h-screen bg-surface text-ink">
    <!-- ---------------- 顶栏 ---------------- -->
    <header
      class="sticky top-0 z-20 flex h-12 items-center gap-3 border-b border-line bg-panel px-4"
    >
      <RouterLink :to="{ name: 'replay' }" :class="[btn, 'shrink-0']" title="回到复盘画布">
        ← 复盘
      </RouterLink>
      <h1 class="text-[15px] font-semibold tracking-wide">交易记录</h1>
      <span class="text-[11px] text-faint">v{{ store.me?.version ?? '' }}</span>
      <span class="flex items-center gap-1.5 text-[12px] text-dim">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusColor" />
        {{ statusText }}
      </span>

      <div class="ml-auto flex min-w-0 items-center gap-2">
        <span
          class="hidden max-w-[22rem] truncate text-[11px] text-faint lg:inline"
          :title="store.recordingDir"
        >
          记录目录 {{ store.recordingDir || '（未配置）' }}
        </span>
        <button type="button" :class="btn" title="重新扫描记录目录" @click="store.listRecordings()">
          刷新
        </button>
        <button
          type="button"
          :class="btn"
          :title="`切换到${themeLabel}模式`"
          @click="store.toggleTheme()"
        >
          {{ themeLabel }}
        </button>
      </div>
    </header>

    <main class="mx-auto flex w-full max-w-[1440px] flex-col gap-3 px-4 py-3">
      <!-- ---------------- 筛选条 ---------------- -->
      <section
        class="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-panel px-3 py-2"
      >
        <span class="text-[11px] text-faint">筛选</span>
        <select v-model="filter.symbol" :class="field" title="按标的筛选">
          <option value="">全部标的</option>
          <option v-for="s in symbols" :key="s.symbol" :value="s.symbol">
            {{ s.symbol }}（{{ s.count }}）
          </option>
        </select>
        <select v-model="filter.side" :class="field" title="按方向筛选">
          <option value="all">多空不限</option>
          <option value="long">只多头</option>
          <option value="short">只空头</option>
        </select>
        <select v-model="filter.status" :class="field" title="按是否平仓筛选">
          <option value="all">全部状态</option>
          <option value="closed">已平仓</option>
          <option value="open">持仓中</option>
        </select>
        <label class="flex items-center gap-1 text-[11px] text-dim">
          从
          <input v-model="filter.from" type="date" :class="field" title="按交易时间（入场）筛选" />
        </label>
        <label class="flex items-center gap-1 text-[11px] text-dim">
          到
          <input v-model="filter.to" type="date" :class="field" title="按交易时间（入场）筛选" />
        </label>
        <input
          v-model="filter.keyword"
          type="search"
          :class="[field, 'w-40']"
          placeholder="搜名称 / 标的 / 操作者"
        />
        <button
          v-if="hasFilter"
          type="button"
          class="text-[11px] text-accent hover:underline"
          @click="resetFilter"
        >
          清除筛选
        </button>
        <span class="ml-auto tnum text-[11px] text-faint">
          {{ filtered.length }} / {{ all.length }} 笔 · {{ sizeText(totalSize) }}
        </span>
      </section>

      <!-- ---------------- 一笔记录都没有：讲清楚这些记录是怎么来的 ---------------- -->
      <section
        v-if="all.length === 0"
        class="rounded-lg border border-line bg-panel px-5 py-8 text-[13px] leading-7 text-dim"
      >
        <h2 class="mb-1 text-[15px] font-semibold text-ink">还没有交易记录</h2>
        <p>
          <b class="text-ink">开仓的那一刻自动开始记，仓位平掉的那一刻自动收尾</b>
          —— 不需要点任何开关，也不会有「正在录制」的提示。记下的是这一笔从进场到出场的
          全部操作（【下一根】、画图、挂单、语音），回放时一格一格演给你看。
        </p>
        <p class="mt-2 text-[12px] text-faint">
          记录落在 <span class="tnum">{{ store.recordingDir || '（未配置记录目录）' }}</span
          >； 做几笔模拟交易再回来，这一页会自动出现统计与图表。
        </p>
        <RouterLink
          :to="{ name: 'replay' }"
          class="mt-4 inline-block text-[13px] text-accent hover:underline"
        >
          ← 去复盘做一笔
        </RouterLink>
      </section>

      <template v-else>
        <!-- ---------------- KPI ---------------- -->
        <section class="grid grid-cols-2 gap-2 md:grid-cols-4">
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">交易笔数</div>
            <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold text-ink">
              {{ agg.count }}
            </div>
            <div class="text-[10px] text-faint">
              {{ agg.closed }} 已平仓 · {{ agg.open }} 持仓中
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">累计盈亏</div>
            <div
              class="tnum mt-0.5 text-[20px] leading-6 font-semibold"
              :class="tone(agg.totalPnl)"
            >
              {{ moneyText(agg.totalPnl) }}
            </div>
            <div class="tnum text-[10px] text-faint">期望 {{ pnlText(agg.avgPnl) }} / 笔</div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">胜率</div>
            <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold text-ink">
              {{ agg.closed ? pctText(agg.winRate) : '—' }}
            </div>
            <div class="tnum text-[10px] text-faint">
              {{ agg.wins }} 胜 / {{ agg.losses }} 负 / {{ agg.flats }} 平
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">盈亏比</div>
            <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold text-ink">
              {{ Number.isFinite(agg.profitFactor) ? agg.profitFactor.toFixed(2) : '∞' }}
            </div>
            <div class="tnum text-[10px] text-faint">
              {{ moneyText(agg.grossWin) }} / {{ moneyText(agg.grossLoss) }}
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">最大回撤</div>
            <div
              class="tnum mt-0.5 text-[20px] leading-6 font-semibold text-emerald-600 dark:text-emerald-400"
            >
              {{ moneyText(agg.maxDrawdown) }}
            </div>
            <div class="tnum text-[10px] text-faint">
              <template v-if="agg.ddFrom > 0">第 {{ agg.ddFrom }} → {{ agg.ddTo }} 笔</template>
              <template v-else>没有回撤</template>
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">平均持仓</div>
            <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold text-ink">
              {{ agg.avgBars }} <span class="text-[11px] font-normal text-faint">根</span>
            </div>
            <div class="tnum text-[10px] text-faint">
              共 {{ agg.totalBars }} 根 · {{ agg.totalFills }} 次成交
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">单笔最佳 / 最差</div>
            <div class="tnum mt-0.5 text-[16px] leading-6 font-semibold">
              <span :class="tone(agg.best?.rec.stats.realizedPnl ?? 0)">
                {{ agg.best ? pnlText(agg.best.rec.stats.realizedPnl) : '—' }}
              </span>
              <span class="mx-1 text-faint">/</span>
              <span :class="tone(agg.worst?.rec.stats.realizedPnl ?? 0)">
                {{ agg.worst ? pnlText(agg.worst.rec.stats.realizedPnl) : '—' }}
              </span>
            </div>
            <div class="truncate text-[10px] text-faint">
              {{ agg.best?.time ?? ''
              }}<template v-if="agg.worst"> · {{ agg.worst.time }}</template>
            </div>
          </div>
          <div class="rounded-lg border border-line bg-panel px-3 py-2">
            <div class="text-[11px] text-faint">连胜 / 连亏</div>
            <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold">
              <span class="text-red-600 dark:text-red-400">{{ agg.maxWinStreak }}</span>
              <span class="mx-1 text-faint">/</span>
              <span class="text-emerald-600 dark:text-emerald-400">{{ agg.maxLossStreak }}</span>
            </div>
            <div class="text-[10px] text-faint">最长连续盈利 / 亏损笔数</div>
          </div>
        </section>

        <!-- ---------------- 图表 ---------------- -->
        <RecordsCharts v-if="agg.count > 0" :agg="agg" />

        <!-- 筛选之后一笔都不剩：不要把 KPI 的 0 当成「没有交易」 -->
        <section
          v-else
          class="rounded-lg border border-line bg-panel px-4 py-10 text-center text-[13px] text-dim"
        >
          没有符合筛选条件的记录。
          <button type="button" class="text-accent hover:underline" @click="resetFilter">
            清除筛选
          </button>
        </section>

        <!-- ---------------- 明细 ---------------- -->
        <section class="rounded-lg border border-line bg-panel">
          <header class="flex items-center gap-2 border-b border-line px-3 py-2">
            <h2 class="text-[12px] font-medium text-ink">交易明细</h2>
            <span class="text-[10px] text-faint">
              共 {{ agg.totalSteps }} 步操作 · 点【回放】回到画布逐格看这一笔
            </span>
            <span
              v-if="agg.bySymbol.length > 1"
              class="ml-auto flex items-center gap-2 text-[10px] text-faint"
            >
              <span v-for="s in agg.bySymbol" :key="s.symbol" class="tnum">
                {{ s.symbol }}
                <b :class="tone(s.pnl)">{{ pnlText(s.pnl) }}</b>
                <span class="text-faint">({{ s.count }} 笔)</span>
              </span>
            </span>
          </header>

          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-[11px]">
              <thead class="bg-elev/60">
                <tr class="border-b border-line">
                  <th :class="th">
                    <button type="button" class="hover:text-accent" @click="sortBy('time')">
                      交易时间 {{ arrow('time') }}
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
                  <th :class="[th, 'text-right']">成交</th>
                  <th :class="th">状态</th>
                  <th :class="[th, 'text-right']">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in rows"
                  :key="row.rec.id"
                  class="border-b border-line/60 transition last:border-0 hover:bg-elev/60"
                >
                  <td :class="[td, 'tnum']">
                    <div class="text-ink">{{ row.time }}</div>
                    <div class="text-[10px] text-faint">
                      → {{ exitText(row.rec) }} · 记于 {{ clock(row.rec.createdAt) }}
                    </div>
                  </td>
                  <td :class="[td, 'text-dim']">{{ row.rec.symbol }}</td>
                  <td :class="td">
                    <span
                      :class="
                        row.rec.stats.side === 'long'
                          ? 'text-red-600 dark:text-red-400'
                          : 'text-emerald-600 dark:text-emerald-400'
                      "
                    >
                      {{ dirText(row.rec) }}
                    </span>
                    <span class="tnum ml-1 text-[10px] text-faint"
                      >峰值 {{ row.rec.stats.maxQuantity }}</span
                    >
                  </td>
                  <td :class="[td, 'tnum text-dim']">
                    {{ row.rec.stats.entryPrice || '—' }}
                    <span class="text-faint">→</span>
                    {{ row.rec.stats.exitPrice || '—' }}
                  </td>
                  <td :class="[td, 'tnum text-right font-medium', tone(row.rec.stats.realizedPnl)]">
                    {{ pnlText(row.rec.stats.realizedPnl) }}
                  </td>
                  <td :class="[td, 'tnum text-right', tone(row.cum)]">{{ pnlText(row.cum) }}</td>
                  <td :class="[td, 'tnum text-right']">
                    <span :class="tone(row.rec.stats.mfe)">{{ pnlText(row.rec.stats.mfe) }}</span>
                    <span class="text-faint"> / </span>
                    <span :class="tone(row.rec.stats.mae)">{{ pnlText(row.rec.stats.mae) }}</span>
                  </td>
                  <td :class="[td, 'tnum text-right text-dim']">
                    {{ row.rec.stats.barsHeld }} 根
                    <span class="text-[10px] text-faint">/ {{ row.rec.stepCount }} 步</span>
                  </td>
                  <td :class="[td, 'tnum text-right text-faint']">{{ row.rec.stats.fills }}</td>
                  <td :class="td">
                    <span
                      v-if="row.rec.stats.closed"
                      class="rounded bg-elev px-1 text-[10px] text-dim"
                    >
                      已平仓
                    </span>
                    <span
                      v-else
                      class="rounded bg-sky-500/15 px-1 text-[10px] text-sky-600 dark:text-sky-300"
                      title="这一笔还没平仓：记录停在最后一次操作上（可能实例被删、程序退出，或还在持仓中）"
                    >
                      持仓中
                    </span>
                  </td>
                  <td :class="[td, 'text-right']">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        type="button"
                        class="rounded border border-line px-1.5 py-0.5 text-[11px] text-dim hover:border-accent hover:text-accent"
                        title="回到复盘画布，逐格回放这一笔"
                        @click="replay(row.rec)"
                      >
                        回放
                      </button>
                      <button
                        type="button"
                        class="rounded px-1.5 py-0.5 text-[11px]"
                        :class="
                          armed === row.rec.id
                            ? 'bg-red-600 text-white'
                            : 'text-faint hover:text-red-600 dark:hover:text-red-400'
                        "
                        :title="
                          armed === row.rec.id ? '再点一下确认删除（不可撤销）' : '删除这笔交易记录'
                        "
                        @click="armDelete(row.rec)"
                      >
                        {{ armed === row.rec.id ? '确认删除' : '删除' }}
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <!--
          统计口径：这一页每个数字怎么来的，写在能用肉眼看到的地方。
          口径只该有一份解释 —— 后端算一笔（trade.go），这里只解释怎么汇总。
        -->
        <details
          class="rounded-lg border border-line bg-panel px-3 py-2 text-[11px] leading-5 text-dim"
        >
          <summary class="cursor-pointer select-none text-[12px] font-medium text-ink">
            统计口径
          </summary>
          <ul class="mt-2 grid list-disc gap-x-6 gap-y-1 pl-4 md:grid-cols-2">
            <li>
              <b class="text-ink">一笔 = 一次完整仓位</b
              >：从空仓变成有仓开始记，回到空仓时收尾。加减仓不另记一笔。
            </li>
            <li>
              <b class="text-ink">盈亏</b
              >：后端的已实现盈亏（成交撮合算出来的钱），这一页只求和，不重算。
            </li>
            <li>
              <b class="text-ink">胜率</b> = 盈利笔数 ÷ <b>已平仓</b>笔数。还在持仓中的不计入分母。
            </li>
            <li><b class="text-ink">盈亏比</b> = 总盈利 ÷ |总亏损|。没有亏损单时显示 ∞。</li>
            <li><b class="text-ink">期望</b> = 累计盈亏 ÷ 笔数，就是「随便抽一笔平均赚多少」。</li>
            <li>
              <b class="text-ink">最大回撤</b>：按交易时间排序后，累计曲线上「峰值 →
              之后最低点」的最大落差。
            </li>
            <li>
              <b class="text-ink">MFE / MAE</b>：持仓期间最大有利 /
              最大不利波动（后端按每根的最高最低算）。
            </li>
            <li>
              <b class="text-ink">持仓根数</b>：这一笔横跨了几根 K
              线；「步」是操作次数，两者不是一回事。
            </li>
          </ul>
        </details>
      </template>
    </main>
  </div>
</template>
