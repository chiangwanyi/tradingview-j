<script setup lang="ts">
import { computed } from 'vue'

import { moneyText, pctText, pnlText } from '@/lib/records'
import type { RecordAggregate } from '@/lib/records'

/**
 * 统计 KPI 卡片（日历的月概览、当日详情、统计页共用）。
 *
 * **这一块不算钱**：所有数字都来自 `aggregate()` 的求和，而每一笔的盈亏本身由
 * 后端算（见 lib/records.ts 的说明）。所以这里怎么排版都不会出现「页面上的钱
 * 和记录里的钱对不上」。
 *
 * 八张卡片的顺序按「先看结论、再看细节」排：笔数 → 钱 → 胜率 → 盈亏比 →
 * 回撤 → 持仓 → 极值 → 连胜。换位置之前先想一遍：用户扫一眼最想知道哪三个数。
 */
const props = defineProps<{
  agg: RecordAggregate
  /** 卡片群的语境，例如「本月」「当天」。只影响第一张卡的副标题 */
  scope?: string
}>()

function tone(n: number): string {
  if (n > 1e-6) return 'text-up'
  if (n < -1e-6) return 'text-down'
  return 'text-ink'
}

const scopeText = computed(() => props.scope ?? '')
const card = 'rounded-lg border border-line bg-panel px-3 py-2'
const label = 'text-[12px] text-faint'
const value = 'tnum mt-0.5 text-[20px] leading-6 font-semibold'
const sub = 'tnum text-[11px] text-faint'
</script>

<template>
  <section class="grid grid-cols-2 gap-2 md:grid-cols-4 xl:grid-cols-4">
    <div :class="card">
      <div :class="label">交易笔数{{ scopeText }}</div>
      <div :class="[value, 'text-ink']">{{ agg.count }}</div>
      <div :class="sub">
        开仓 {{ agg.count }} · 已平 {{ agg.closed }}
        <template v-if="agg.open"> · 持仓中 {{ agg.open }}</template>
      </div>
    </div>

    <div :class="card">
      <div :class="label">累计盈亏{{ scopeText }}</div>
      <div :class="[value, tone(agg.totalPnl)]">{{ moneyText(agg.totalPnl) }}</div>
      <div :class="sub">期望 {{ pnlText(agg.avgPnl) }} / 笔</div>
    </div>

    <div :class="card">
      <div :class="label">胜率</div>
      <div :class="[value, 'text-ink']">{{ agg.closed ? pctText(agg.winRate) : '—' }}</div>
      <div :class="sub">{{ agg.wins }} 胜 / {{ agg.losses }} 负 / {{ agg.flats }} 平</div>
    </div>

    <div :class="card">
      <div :class="label">盈亏比</div>
      <div :class="[value, 'text-ink']">
        {{ Number.isFinite(agg.profitFactor) ? agg.profitFactor.toFixed(2) : '∞' }}
      </div>
      <div :class="sub">{{ moneyText(agg.grossWin) }} / {{ moneyText(agg.grossLoss) }}</div>
    </div>

    <div :class="card">
      <div :class="label">最大回撤</div>
      <div :class="[value, tone(agg.maxDrawdown)]">{{ moneyText(agg.maxDrawdown) }}</div>
      <div :class="sub">
        <template v-if="agg.ddFrom > 0">第 {{ agg.ddFrom }} → {{ agg.ddTo }} 笔</template>
        <template v-else>没有回撤</template>
      </div>
    </div>

    <div :class="card">
      <div :class="label">平均持仓</div>
      <div :class="[value, 'text-ink']">
        {{ agg.avgBars }} <span class="text-[12px] font-normal text-faint">根</span>
      </div>
      <div :class="sub">共 {{ agg.totalBars }} 根 · {{ agg.totalFills }} 次成交</div>
    </div>

    <div :class="card">
      <div :class="label">单笔最佳 / 最差</div>
      <div class="tnum mt-0.5 text-[16px] leading-6 font-semibold">
        <span :class="tone(agg.best?.rec.stats.realizedPnl ?? 0)">
          {{ agg.best ? pnlText(agg.best.rec.stats.realizedPnl) : '—' }}
        </span>
        <span class="mx-1 text-faint">/</span>
        <span :class="tone(agg.worst?.rec.stats.realizedPnl ?? 0)">
          {{ agg.worst ? pnlText(agg.worst.rec.stats.realizedPnl) : '—' }}
        </span>
      </div>
      <div class="truncate text-[11px] text-faint">
        {{ agg.best?.time ?? ''
        }}<template v-if="agg.worst"> · {{ agg.worst.time }}</template>
      </div>
    </div>

    <div :class="card">
      <div :class="label">连胜 / 连亏</div>
      <div class="tnum mt-0.5 text-[20px] leading-6 font-semibold">
        <span class="text-up">{{ agg.maxWinStreak }}</span>
        <span class="mx-1 text-faint">/</span>
        <span class="text-down">{{ agg.maxLossStreak }}</span>
      </div>
      <div class="text-[11px] text-faint">最长连续盈利 / 亏损笔数</div>
    </div>
  </section>
</template>
