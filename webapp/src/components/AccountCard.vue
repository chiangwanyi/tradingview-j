<script setup lang="ts">
import { computed } from 'vue'

import Icon from './Icon.vue'
import {
  lossLine,
  profitTarget,
  shortInstant,
  shortTime,
  targetProgress,
  targetRaised,
} from '@/data/accounts'
import type { Account } from '@/data/accounts'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'

/**
 * 一张考核卡。版式照参考图的账户卡：身份 → 三个状态小标签 → 四个数字 → 进度条 → 动作。
 *
 * 卡片上的入口只有三个：继续交易 / 详情 / 结算今日；外加一颗**只在考核通过时才出现**的
 * 【升级】—— 它是唯一一个「有结论才有动作」的按钮，没达标时整颗不画，
 * 而不是画出来再置灰（置灰的按钮会让人以为点得动、只是坏了）。改名、删除、阶段流水仍在详情页。
 *
 * ⚠️ 「通没通过」用 `a.passed`（后端现算的），**不在这里按 profitTarget / 盈利日自己推**：
 * 那是一份考核口径，推一遍就有了第二份，两份迟早不一致。
 *
 * ⚠️ 盈利目标一律用 `profitTarget(a)`（后端算好的**一致性抬升后**的值），
 * 不能用 `a.rules.profitTarget`：单日赚 2750、一致性 40% 时真实目标是 6875，
 * 而模板上写的是 3000 —— 拿模板的值会显示「还差 40 就过关」，那是错的。
 */
const props = defineProps<{ a: Account; busy?: boolean; pending?: 'settle' | 'promote' | '' }>()
const emit = defineEmits<{ detail: [id: string]; settle: [id: string]; promote: [id: string] }>()

const target = computed(() => profitTarget(props.a))
const progress = computed(() => targetProgress(props.a))
const gained = computed(() => props.a.balance - props.a.baseBalance)
/** 亏损线：口径和交易页一致（含冻结水位），见 `data/accounts.ts` 的 `lossLine`。 */
const floor = computed(() => lossLine(props.a))
/** 目标被一致性抬高了：副行要说明来源，否则「目标怎么突然变了」会变成一个疑问。 */
const raised = computed(() => targetRaised(props.a))

/** 考核通过、能升上去 —— 失败和 PAYOUT 的账户后端给的就是 false，不用在这儿再判一遍。 */
const canPromote = computed(() => props.a.passed === true && props.a.status === 'ACTIVE')
/** 下一阶段叫什么（按钮上说清楚升到哪儿，别只写「升级」）。 */
const nextStage = computed(() => (props.a.stage === 'EVAL' ? 'FUNDED' : 'PAYOUT'))

// ---- 继续交易 ----
/**
 * 开一个**新标签页**去交易（`/trade/:id`）。
 *
 * 为什么不跳转当前页：一人可能同时盯两三个账户，浏览器标签天然支持并排、各自记住
 * 缩放和手上的工具；关掉一个也不影响另一个。URL 里带账户 id，所以刷新、收藏、
 * 粘给别人都能回到同一个账户。
 */
function onTrade(): void {
  window.open(`/trade/${encodeURIComponent(props.a.id)}`, '_blank', 'noopener')
}
</script>

<template>
  <article class="flex flex-col rounded-xl border border-line bg-panel shadow-[0_1px_2px_rgba(15,23,42,0.04)]">
    <header class="flex items-start justify-between gap-2 px-4 pt-4">
      <div class="min-w-0">
        <h3 class="truncate text-[15px] font-semibold tracking-wide text-ink">
          {{ a.name }}
        </h3>
        <p class="mt-0.5 truncate text-[12px] text-faint tnum">起点 {{ shortTime(a.startBarTime) }}</p>
      </div>
      <span
        class="shrink-0 rounded border border-line bg-elev px-1.5 py-0.5 text-[11px] tracking-wider text-dim"
        >{{ stageLabel(a.stage) }}</span
      >
    </header>

    <!-- 三个状态小标签：标的 / 回撤口径 / 在考核中或已失败 / 本阶段起点 -->
    <div class="mt-2.5 flex flex-wrap gap-1 px-4">
      <!-- 标的：建户时定死，卡片上给一个能一眼认出来的位置（同名账户靠它区分） -->
      <span
        class="rounded-sm border border-accent/30 bg-accent-soft px-1.5 py-0.5 text-[10px] tracking-wider text-accent-ink uppercase"
        title="这个账户只做这一个标的，创建后不可切换"
      >
        {{ a.symbolId ?? '未绑定标的' }}
      </span>
      <span class="rounded-sm border border-line px-1.5 py-0.5 text-[10px] tracking-wider text-faint uppercase">
        {{ drawdownLabel(a.rules.drawdownType) }}
      </span>
      <span
        class="rounded-sm border px-1.5 py-0.5 text-[10px] tracking-wider uppercase"
        :class="
          a.status !== 'ACTIVE'
            ? 'border-red-200 bg-red-50 text-red-700'
            : canPromote
              ? 'border-amber-300 bg-amber-50 text-amber-800'
              : 'border-accent/30 bg-accent-soft text-accent-ink'
        "
      >
        {{ a.status !== 'ACTIVE' ? '已失败' : canPromote ? '已达标 · 待升级' : '进行中' }}
      </span>
      <span class="rounded-sm border border-line px-1.5 py-0.5 text-[10px] tracking-wider text-faint uppercase">
        创建于 {{ shortInstant(a.stageStartedAt) }}
      </span>
    </div>

    <div class="mt-3 grid grid-cols-2 gap-x-3 gap-y-3 border-t border-line px-4 py-3.5">
      <div>
        <p class="text-[11px] tracking-wider text-faint uppercase">账户余额</p>
        <p class="tnum text-[18px] leading-tight font-semibold text-ink">{{ usd(a.balance) }}</p>
        <p class="tnum text-[11px] text-faint">
          起点 {{ usd(a.baseBalance) }} ·
          <span :class="gained >= 0 ? 'text-accent-ink' : 'text-red-600'">
            {{ gained >= 0 ? '+' : '' }}{{ usd(gained) }}
          </span>
        </p>
      </div>
      <div>
        <p class="text-[11px] tracking-wider text-faint uppercase">盈利目标</p>
        <p class="tnum text-[18px] leading-tight font-semibold text-ink">
          {{ usd(target) }}
        </p>
        <p class="tnum text-[11px] text-faint">
          {{ target == null ? '本阶段无目标' : `还差 ${usd(Math.max(0, target - gained))}` }}
        </p>
        <!-- 一致性抬升：目标 > 模板上的那个数时说明原因，否则会被当成 bug -->
        <p v-if="raised" class="tnum text-[11px] text-accent-ink" title="单日最大净盈利 ÷ 一致性比例">
          一致性抬升 · 单日最大 {{ usd(a.maxDayNetBalance) }}
        </p>
      </div>
      <div>
        <p class="text-[11px] tracking-wider text-faint uppercase">最大回撤</p>
        <p class="tnum text-[14px] font-medium text-ink">{{ usd(a.rules.maxDrawdown) }}</p>
        <p class="tnum text-[11px] text-faint">亏损线 {{ usd(floor) }}</p>
      </div>
      <div>
        <p class="text-[11px] tracking-wider text-faint uppercase">一致性上限</p>
        <p class="tnum text-[14px] font-medium text-ink">
          {{ a.rules.consistencyRatio == null ? '—' : `≤${pct(a.rules.consistencyRatio)}` }}
        </p>
        <p class="tnum text-[11px] text-faint">
          {{ a.rules.minProfitDays == null ? '不查盈利日' : `盈利日 ${a.rules.minProfitDays} 天` }}
        </p>
      </div>
    </div>

    <!-- 进度条只在有目标时出现：FUNDED / PAYOUT 没有「差多少达标」这回事，画一根空槽会误导 -->
    <div v-if="progress != null" class="px-4 pb-3">
      <div class="h-1 w-full overflow-hidden rounded-full bg-elev">
        <div class="h-full rounded-full bg-accent" :style="{ width: `${progress * 100}%` }" />
      </div>
      <div class="mt-1 flex justify-between text-[10px] tracking-wider text-faint uppercase tnum">
        <span>起点 {{ usd(a.baseBalance) }}</span>
        <span>{{ pct(progress) }}</span>
        <span>目标 {{ usd(a.baseBalance + (target ?? 0)) }}</span>
      </div>
    </div>

    <div class="mt-auto grid grid-cols-2 gap-1.5 px-4 pb-4">
      <button
        type="button"
        class="flex items-center justify-center gap-1 rounded-md bg-accent py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink"
        title="在新标签页打开这个账户的 K 线画布"
        @click="onTrade"
      >
        <Icon name="right" :size="13" />继续交易
      </button>
      <button
        type="button"
        class="rounded-md border border-line py-2 text-[13px] text-dim transition-colors hover:border-line-strong hover:text-ink"
        @click="emit('detail', a.id)"
      >
        详情
      </button>
      <!-- 升级：**只在考核通过时出现**（`passed` 由后端重放算出来）。
           没通过时整颗不画 —— 画出来再置灰，人会以为「点得动、只是坏了」，
           而真实情况是「还没考完」。 -->
      <button
        v-if="canPromote"
        type="button"
        class="col-span-2 flex items-center justify-center gap-1.5 rounded-md bg-accent py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink disabled:opacity-50"
        title="余额会重置回下一阶段的账户规模（本阶段赚的钱不带过去）"
        :disabled="busy"
        @click="emit('promote', a.id)"
      >
        <Icon name="check" :size="13" />
        {{ pending === 'promote' ? '升级中…' : `考核通过 · 升级到 ${nextStage}` }}
      </button>
      <!-- 只在考核中给：失败的账户没有「今天的账」可结 -->
      <button
        v-if="a.status === 'ACTIVE'"
        type="button"
        class="col-span-2 rounded-md border border-line py-2 text-[13px] text-dim transition-colors hover:border-line-strong hover:text-ink disabled:opacity-50"
        title="一步推到下一个交易日，结算上一个交易日（日终峰值 → 回撤线上移）；有持仓或挂单时不让用"
        :disabled="busy"
        @click="emit('settle', a.id)"
      >
        {{ pending === 'settle' ? '结算中…' : '结算今日' }}
      </button>
    </div>
  </article>
</template>
