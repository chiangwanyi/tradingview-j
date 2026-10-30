<script setup lang="ts">
import { computed, ref } from 'vue'

import Icon from './Icon.vue'
import { shortInstant, shortTime, targetProgress } from '@/data/accounts'
import type { Account } from '@/data/accounts'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'

/**
 * 一张考核卡。版式照参考图的账户卡：身份 → 三个状态小标签 → 四个数字 → 进度条 → 动作。
 *
 * 卡片只留两个入口：继续交易 / 详情。升级、改名、删除、阶段流水全在详情页 ——
 * 卡片是「一眼看进度」的地方，写操作不该跟数字抢位置。
 */
const props = defineProps<{ a: Account }>()
const emit = defineEmits<{ trade: [id: string]; detail: [id: string] }>()

const progress = computed(() => targetProgress(props.a))
const gained = computed(() => props.a.balance - props.a.baseBalance)
/** 亏损线：跟踪日终口径下就是「日终峰值 − 最大回撤」。没有回撤规则就不编一个数。 */
const lossLine = computed(() =>
  props.a.rules.maxDrawdown == null ? null : props.a.eodPeakBalance - props.a.rules.maxDrawdown,
)

// ---- 继续交易（占位）----
/** 交易界面还没做：点了就在本卡浮一行说明，不弹窗、不跳走，免得以为点坏了。 */
const notice = ref('')
let noticeTimer: ReturnType<typeof setTimeout> | undefined

function onTrade(): void {
  emit('trade', props.a.id)
  notice.value = '交易界面还没做好，这里先占个位。'
  clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => {
    notice.value = ''
  }, 2600)
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

    <!-- 三个状态小标签：回撤口径 / 在考核中或已失败 / 本阶段起点 -->
    <div class="mt-2.5 flex flex-wrap gap-1 px-4">
      <span class="rounded-sm border border-line px-1.5 py-0.5 text-[10px] tracking-wider text-faint uppercase">
        {{ drawdownLabel(a.rules.drawdownType) }}
      </span>
      <span
        class="rounded-sm border px-1.5 py-0.5 text-[10px] tracking-wider uppercase"
        :class="
          a.status === 'ACTIVE'
            ? 'border-accent/30 bg-accent-soft text-accent-ink'
            : 'border-red-200 bg-red-50 text-red-700'
        "
      >
        {{ a.status === 'ACTIVE' ? '进行中' : '已失败' }}
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
          {{ usd(a.rules.profitTarget) }}
        </p>
        <p class="tnum text-[11px] text-faint">
          {{ a.rules.profitTarget == null ? '本阶段无目标' : `还差 ${usd(Math.max(0, a.rules.profitTarget - gained))}` }}
        </p>
      </div>
      <div>
        <p class="text-[11px] tracking-wider text-faint uppercase">最大回撤</p>
        <p class="tnum text-[14px] font-medium text-ink">{{ usd(a.rules.maxDrawdown) }}</p>
        <p class="tnum text-[11px] text-faint">亏损线 {{ usd(lossLine) }}</p>
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
        <span>目标 {{ usd(a.baseBalance + (a.rules.profitTarget ?? 0)) }}</span>
      </div>
    </div>

    <p
      v-if="notice"
      class="mx-4 mb-2 rounded border border-line bg-elev px-2 py-1 text-[12px] text-dim"
    >
      {{ notice }}
    </p>

    <div class="mt-auto grid grid-cols-2 gap-1.5 px-4 pb-4">
      <button
        type="button"
        class="flex items-center justify-center gap-1 rounded-md bg-accent py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink"
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
    </div>
  </article>
</template>
