<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import Icon from '@/components/Icon.vue'
import TradingCalendar from '@/components/TradingCalendar.vue'
import * as api from '@/data/accounts'
import { lossLine, profitTarget, shortInstant, shortTime, stageBadge, targetRaised } from '@/data/accounts'
import type { Account, DayPnl, StageLog } from '@/data/accounts'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'
import { errorText } from '@/data/store'

/**
 * 账户详情（整页，不是抽屉）。版式照参考图自上而下：
 *
 *   权益进度条 → 交易日历 → 规则快照 → 账户身份 → 阶段流水 → 动作。
 *
 * 卡片上收走的写操作全在这里：升级 / 改名 / 删除 + 阶段流水。
 *
 * 数据自己拉（`GET /api/pf-accounts/{id}` + `/{id}/stages` + `/{id}/calendar`），
 * 不吃列表里那份拷件 —— 从列表点进来时它可能已经是几十秒前的余额。
 *
 * 「继续交易」在卡片和这里都有：它开一个新标签页（`/trade/:id`），不改变本页状态 ——
 * 详情页留着不动，交易页在另一个标签里跑，两边都刷新也不会互相打断。
 */
const props = defineProps<{ id: string }>()
const emit = defineEmits<{ back: [] }>()

const a = ref<Account | null>(null)
const logs = ref<StageLog[]>([])
/** 交易日历：后端按交易日分好组的一整天一整天的汇总。 */
const days = ref<DayPnl[]>([])
const calLoading = ref(true)
const loading = ref(true)
const err = ref('')
const busy = ref(false)

const gained = computed(() => (a.value ? a.value.balance - a.value.baseBalance : 0))
/** 亏损线：口径和交易页一致（含冻结水位），见 `data/accounts.ts` 的 `lossLine`。 */
const floor = computed(() => (a.value ? lossLine(a.value) : null))
/**
 * **真实**盈利目标（一致性抬高之后）。不用 `a.rules.profitTarget`：
 * 单日赚 2750、一致性 40% 时目标是 6875，模板上那个 3000 会让人以为还差 40 就过关。
 */
const target = computed(() => (a.value ? profitTarget(a.value) : null))
/** 目标被一致性抬高了：规则快照那块要同时给出模板值和抬升来源。 */
const raised = computed(() => (a.value ? targetRaised(a.value) : false))

/** 阶段标签：同一份口径，和首页卡片上那个一致（「考核 → 出金号」）。 */
const badge = computed(() => (a.value ? stageBadge(a.value) : null))

/**
 * 进度条的三段：**左端 = 亏损线，右端 = 目标**，填充到当前余额。
 *
 * 为什么不从 0 画到目标：这条轨道的意义是「还剩多少空间」，左端必须是那道
 * 会被打穿的线（亏损线），不是账户规模。参考图上左端那根 `START` 就是它。
 *
 * 没有盈利目标（FUNDED / PAYOUT）时右端退化成**日终峰值**：那时要盯的不是
 * 「赚够多少」，而是「余额离峰值和亏损线各有多远」，峰值就是那条轨道的满格。
 */
const bar = computed(() => {
  const acc = a.value
  if (!acc) return null
  const hasTarget = target.value != null
  const start = floor.value ?? acc.baseBalance
  const end = hasTarget
    ? acc.baseBalance + (target.value ?? 0)
    : Math.max(acc.eodPeakBalance, acc.balance, start)
  const span = end - start
  const fill = span > 0 ? Math.min(1, Math.max(0, (acc.balance - start) / span)) : 0
  return { hasTarget, start, end, fill }
})

/** 余额还能往下走多少（正数 = 缓冲）。没有回撤规则就是 null，不编一个数。 */
const buffer = computed(() =>
  a.value && floor.value != null ? Math.round((a.value.balance - floor.value) * 100) / 100 : null,
)

/** 距目标还差多少；没有目标就是 null。目标用一致性抬高后的真实值。 */
const remaining = computed(() => {
  const t = target.value
  if (t == null) return null
  return Math.max(0, Math.round((t - gained.value) * 100) / 100)
})

/** `+$52.50` / `-$40.00`：金额一律带符号，颜色只是辅助。 */
function signed(n: number): string {
  const text = `$${Math.abs(n).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
  return n > 0.005 ? `+${text}` : n < -0.005 ? `-${text}` : text
}

/** 只刷新数据、不清空已有内容：改名 / 升级后原地换个数字，别整页闪一下。 */
function load(): Promise<void> {
  calLoading.value = days.value.length === 0
  return Promise.all([api.get(props.id), api.stages(props.id), api.calendar(props.id)])
    .then(([acc, l, d]) => {
      a.value = acc
      logs.value = l
      days.value = d
      err.value = ''
    })
    .catch((e: unknown) => {
      err.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
      calLoading.value = false
    })
}

onMounted(load)

// ---- 写操作 ----
function run(fn: () => Promise<unknown>): void {
  busy.value = true
  err.value = ''
  fn()
    .catch((e: unknown) => {
      err.value = errorText(e)
    })
    .finally(() => {
      busy.value = false
    })
}

const editing = ref(false)
const draftName = ref('')

function startRename(): void {
  draftName.value = a.value?.name ?? ''
  editing.value = true
}

function submitRename(): void {
  const name = draftName.value.trim()
  if (!name || !a.value) {
    editing.value = false
    return
  }
  editing.value = false
  run(() => api.rename(a.value!.id, name).then(load))
}

function doPromote(): void {
  const acc = a.value
  if (!acc) return
  const next = acc.stage === 'EVAL' ? 'FUNDED' : 'PAYOUT'
  // 没达标也允许升（后端只要求 ACTIVE 且不是 PAYOUT），但问句里要把这件事说明白：
  // 一旦升上去，本阶段赚的钱就被拨回账户规模，退不回来。
  const warn = acc.passed ? '' : '⚠️ 这个账户还没达成本阶段的升级条件。'
  if (!confirm(`${warn}把「${acc.name}」升到 ${next}？余额会重置回新阶段的账户规模。`)) return
  run(() => api.promote(acc.id).then(load))
}

function doRemove(): void {
  if (!a.value) return
  if (!confirm(`删掉「${a.value.name}」？它的阶段流水会一起删掉（不可恢复），成交和图形保留。`)) return
  run(() => api.remove(a.value!.id).then(() => emit('back')))
}

/** 新标签页打开交易页（和卡片上那颗按钮同一个行为，两处入口一个目的）。 */
function openTrade(): void {
  if (!a.value) return
  window.open(`/trade/${encodeURIComponent(a.value.id)}`, '_blank', 'noopener')
}
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-7 pb-4">
      <button
        type="button"
        class="flex items-center gap-1 text-[13px] text-dim transition-colors hover:text-ink"
        @click="emit('back')"
      >
        <Icon name="left" :size="14" />返回账户总览
      </button>

      <div v-if="a" class="mt-3 flex flex-wrap items-center gap-2">
        <input
          v-if="editing"
          v-model="draftName"
          class="w-[220px] rounded-md border border-line bg-panel px-2 py-1 text-[22px] font-semibold text-ink outline-none focus:border-accent/60"
          @keyup.enter="submitRename"
          @keyup.esc="editing = false"
        />
        <h1 v-else class="text-[24px] font-semibold tracking-tight text-ink">{{ a.name }}</h1>

        <span
          v-if="badge"
          class="rounded border px-1.5 py-0.5 text-[11px] tracking-wider"
          :class="
            badge.earned
              ? 'border-accent/40 bg-accent-soft text-accent-ink'
              : 'border-line bg-elev text-dim'
          "
          :title="badge.hint"
        >
          {{ badge.text }}
        </span>
        <span
          class="rounded border px-1.5 py-0.5 text-[11px] tracking-wider uppercase"
          :class="
            a.status === 'ACTIVE'
              ? 'border-accent/30 bg-accent-soft text-accent-ink'
              : 'border-red-200 bg-red-50 text-red-700'
          "
        >
          {{ a.status === 'ACTIVE' ? '进行中' : '已失败' }}
        </span>
        <!-- 已达标：后端重放算出来的结论（`passed`），页面顶部就要能看到，
             不能让人翻到最底下的按钮才知道考过了 -->
        <span
          v-if="a.passed"
          class="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[11px] tracking-wider text-amber-800"
          title="已达成本阶段升级条件，持仓已平、暂停接受新单"
        >
          已达标 · 待升级
        </span>
        <span class="rounded border border-line px-1.5 py-0.5 text-[11px] tracking-wider text-faint uppercase">
          {{ drawdownLabel(a.rules.drawdownType) }}
        </span>

        <template v-if="editing">
          <button
            type="button"
            class="ml-1 rounded-md border border-accent/40 px-3 py-1 text-[13px] font-medium text-accent-ink hover:bg-accent-soft"
            @click="submitRename"
          >
            保存
          </button>
          <button
            type="button"
            class="rounded-md border border-line px-3 py-1 text-[13px] text-dim hover:border-line-strong"
            @click="editing = false"
          >
            取消
          </button>
        </template>

        <div class="ml-auto flex flex-wrap gap-2">
          <button
            type="button"
            class="flex items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink"
            title="在新标签页打开这个账户的 K 线画布"
            @click="openTrade"
          >
            <Icon name="right" :size="14" />继续交易
          </button>
        </div>
      </div>
      <p v-else class="mt-3 text-[13px] text-faint">{{ loading ? '加载中…' : '账户不存在或已删除。' }}</p>
    </header>

    <p v-if="err" class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700">
      {{ err }}
    </p>

    <div v-if="a" class="flex flex-col gap-3 px-8 pb-10">
      <!-- 1. 权益进度：亏损线 ──── 当前余额 ──── 目标 -->
      <section v-if="bar" class="rounded-xl border border-line bg-panel px-5 py-4">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <p class="text-[11px] tracking-wider text-faint uppercase">本阶段权益进度</p>
          <p class="tnum text-[11px] text-faint">
            {{ bar.hasTarget ? `距目标还差 ${usd(remaining)}` : '本阶段没有盈利目标，盯住亏损线' }}
          </p>
        </div>

        <!-- 轨道：[亏损线 ────── 当前 ────── 目标] -->
        <div class="mt-3 h-2.5 w-full overflow-hidden rounded-full bg-elev">
          <div
            class="h-full rounded-full bg-accent/70 transition-[width] duration-500"
            :style="{ width: `${bar.fill * 100}%` }"
          />
        </div>

        <div class="tnum mt-2 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-[11px] text-faint">
          <span class="flex items-center gap-1.5">
            <span class="h-2 w-2 rounded-full bg-down/50" />
            亏损线 {{ usd(bar.start) }}
          </span>
          <span class="flex items-center gap-1.5 text-dim">
            <span class="h-2 w-2 rounded-full bg-accent" />
            当前 {{ usd(a.balance) }}
          </span>
          <span class="flex items-center gap-1.5">
            {{ bar.hasTarget ? '目标' : '日终峰值' }} {{ usd(bar.end) }}
            <span class="h-2 w-2 rounded-full bg-ink/30" />
          </span>
        </div>

        <div class="mt-4 grid grid-cols-1 gap-4 border-t border-line pt-3 sm:grid-cols-3">
          <div>
            <p class="text-[11px] tracking-wider text-faint uppercase">回撤空间</p>
            <p
              class="tnum text-[20px] leading-tight font-semibold"
              :class="buffer != null && buffer <= 0 ? 'text-down' : 'text-ink'"
            >
              {{ usd(buffer) }}
            </p>
            <p class="tnum text-[11px] text-faint">余额 − 亏损线</p>
          </div>
          <div>
            <p class="text-[11px] tracking-wider text-faint uppercase">账户余额</p>
            <p class="tnum text-[20px] leading-tight font-semibold text-ink">{{ usd(a.balance) }}</p>
            <p class="tnum text-[11px] text-faint">
              起点 {{ usd(a.baseBalance) }} ·
              <span :class="gained >= 0 ? 'text-up' : 'text-down'">{{ signed(gained) }}</span>
            </p>
          </div>
          <div>
            <p class="text-[11px] tracking-wider text-faint uppercase">
              {{ bar.hasTarget ? '距目标' : '已在峰值' }}
            </p>
            <p class="tnum text-[20px] leading-tight font-semibold text-ink">
              {{ bar.hasTarget ? usd(remaining) : usd(a.eodPeakBalance) }}
            </p>
            <p class="tnum text-[11px] text-faint">
              {{ bar.hasTarget ? `盈利目标 ${usd(target)}` : '跟踪回撤的基准' }}
            </p>
            <p v-if="raised" class="tnum text-[11px] text-accent-ink">
              一致性抬升 · 单日最大 {{ usd(a.maxDayNetBalance) }}
            </p>
          </div>
        </div>
      </section>

      <!-- 2. 交易日历：格子 = 一个交易日，点开看当天每一笔 -->
      <TradingCalendar :days="days" :loading="calLoading" />

      <!-- 3. 规则快照：开账户那一刻从模板复制过来的，之后与模板无关 -->
      <div class="grid grid-cols-2 gap-x-8 gap-y-4 rounded-xl border border-line bg-panel px-5 py-4 lg:grid-cols-4">
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">账户规模</p>
          <p class="tnum text-[18px] leading-tight font-semibold text-ink">{{ usd(a.rules.accountSize) }}</p>
          <p class="tnum text-[11px] text-faint">回撤口径 {{ drawdownLabel(a.rules.drawdownType) }}</p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">盈利目标</p>
          <p class="tnum text-[18px] leading-tight font-semibold text-ink">{{ usd(target) }}</p>
          <p class="tnum text-[11px] text-faint">
            {{ target == null ? '本阶段无目标' : `还差 ${usd(remaining)}` }}
          </p>
          <p v-if="raised" class="tnum text-[11px] text-accent-ink" title="单日最大净盈利 ÷ 一致性比例">
            模板 {{ usd(a.rules.profitTarget) }} → 抬到 {{ usd(target) }}
          </p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">最大回撤</p>
          <p class="tnum text-[18px] leading-tight font-semibold text-ink">{{ usd(a.rules.maxDrawdown) }}</p>
          <p class="tnum text-[11px] text-faint">亏损线 {{ usd(floor) }}</p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">一致性上限</p>
          <p class="tnum text-[18px] leading-tight font-semibold text-ink">
            {{ a.rules.consistencyRatio == null ? '—' : `≤${pct(a.rules.consistencyRatio)}` }}
          </p>
          <p class="tnum text-[11px] text-faint">
            {{ a.rules.minProfitDays == null ? '不查盈利日' : `盈利日 ${a.rules.minProfitDays} 天` }}
          </p>
          <p class="tnum text-[11px] text-faint">单日最大 {{ usd(a.maxDayNetBalance) }}</p>
        </div>
      </div>

      <!-- 4. 时间与来源：账户的「身份」都在这里，卡片上放不下 -->
      <dl
        class="grid grid-cols-1 gap-x-8 gap-y-2.5 rounded-xl border border-line bg-panel px-5 py-4 text-[13px] sm:grid-cols-2"
      >
        <div class="flex justify-between gap-4">
          <dt class="text-faint">交易标的</dt>
          <dd class="truncate text-ink" :title="`创建账户时选定，不可切换`">
            {{ a.symbolId ?? '未绑定' }}
            <span class="ml-1 text-[11px] text-faint">（不可切换）</span>
          </dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">起点 K 线</dt>
          <dd class="tnum text-ink">{{ shortTime(a.startBarTime) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">盘面最新 K 线</dt>
          <dd class="tnum text-ink">{{ shortTime(a.lastBarTime) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">日终峰值</dt>
          <dd class="tnum text-ink">{{ usd(a.eodPeakBalance) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">本阶段自</dt>
          <dd class="tnum text-ink">{{ shortInstant(a.stageStartedAt) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">建户时刻</dt>
          <dd class="tnum text-ink">{{ shortInstant(a.startedAt) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">起始日文件</dt>
          <dd class="truncate text-ink">{{ a.startDayFile }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">模板</dt>
          <dd class="truncate text-ink" :title="a.templateId">{{ a.templateId }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">账户 ID</dt>
          <dd class="truncate text-ink" :title="a.id">{{ a.id }}</dd>
        </div>
      </dl>

      <!-- 5. 阶段流水：行数就是身份（1 行 = 建户时就在这个阶段，2 行 = 考过来的） -->
      <div class="rounded-xl border border-line bg-panel px-5 py-4">
        <p class="text-[13px] font-medium text-ink">阶段流水</p>
        <table v-if="logs.length" class="mt-2 w-full text-[12px]">
          <thead>
            <tr class="text-left text-[11px] tracking-wider text-faint uppercase">
              <th class="py-1.5 pr-4 font-normal">阶段</th>
              <th class="py-1.5 pr-4 font-normal">进入</th>
              <th class="py-1.5 pr-4 font-normal">结束</th>
              <th class="py-1.5 pr-4 text-right font-normal">基础余额</th>
              <th class="py-1.5 pr-4 text-right font-normal">期末余额</th>
              <th class="py-1.5 pr-4 text-right font-normal">净额</th>
              <th class="py-1.5 font-normal">怎么结束的</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="l in logs" :key="l.id" class="border-t border-line">
              <td class="py-1.5 pr-4 text-dim">{{ stageLabel(l.stage) }}</td>
              <td class="tnum py-1.5 pr-4 text-ink">{{ shortInstant(l.startedAt) }}</td>
              <td class="tnum py-1.5 pr-4 text-ink">{{ l.endedAt ? shortInstant(l.endedAt) : '进行中' }}</td>
              <td class="tnum py-1.5 pr-4 text-right text-dim">{{ usd(l.baseBalance) }}</td>
              <td class="tnum py-1.5 pr-4 text-right text-dim">{{ usd(l.balanceAtEnd) }}</td>
              <td
                class="tnum py-1.5 pr-4 text-right"
                :class="l.netPnl == null ? 'text-faint' : l.netPnl >= 0 ? 'text-up' : 'text-down'"
              >
                {{ usd(l.netPnl) }}
              </td>
              <td class="py-1.5 text-faint">{{ l.endReason ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="mt-2 text-[12px] text-faint">没有阶段流水。</p>
      </div>

      <!-- 6. 动作：升级 / 改名 / 删除（继续交易在页头，天天要点的那个放上面） -->
      <div class="flex flex-wrap gap-2">
        <!-- 升级：已达标时是主按钮（首页那颗也一样），没达标时留一颗弱按钮 ——
             手工升级仍然允许（后端只要求 ACTIVE 且不是 PAYOUT），但要先看清没达标这件事。 -->
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md px-4 py-2 text-[13px] transition-colors disabled:cursor-not-allowed disabled:opacity-40"
          :class="
            a.passed && a.status === 'ACTIVE' && a.stage !== 'PAYOUT'
              ? 'bg-accent font-medium text-white hover:bg-accent-ink'
              : 'border border-line text-dim hover:border-line-strong hover:text-ink'
          "
          :disabled="busy || a.status === 'FAILED' || a.stage === 'PAYOUT'"
          :title="
            a.stage === 'PAYOUT'
              ? '已是最后阶段'
              : a.passed
                ? '考核已通过：升级会换下一套规则、余额重置回账户规模'
                : '还没达成本阶段的升级条件（后端不会拦，但正常流程是等达标）'
          "
          @click="doPromote"
        >
          <Icon name="check" :size="14" />{{ a.passed ? '考核通过 · 升级' : '升级' }}
        </button>
        <button
          type="button"
          class="rounded-md border border-line px-4 py-2 text-[13px] text-dim transition-colors hover:border-line-strong hover:text-ink"
          :disabled="busy"
          @click="startRename"
        >
          改名
        </button>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-[13px] text-faint transition-colors hover:border-red-200 hover:text-red-700"
          :disabled="busy"
          @click="doRemove"
        >
          <Icon name="trash" :size="14" />删除
        </button>
      </div>

      <!-- 占位：还没接的东西写清楚，别让人以为加载失败 -->
      <div class="rounded-xl border border-dashed border-line px-5 py-4">
        <p class="text-[13px] font-medium text-dim">这一页还差几块</p>
        <ul class="mt-1.5 list-disc space-y-1 pl-5 text-[12px] text-faint">
          <li>权益曲线（日终余额的时间序列，后端还没存）</li>
          <li>当前规则快照的完整字段（单笔手数上限、日亏损上限等）</li>
        </ul>
      </div>
    </div>
  </section>
</template>
