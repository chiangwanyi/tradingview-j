<script setup lang="ts">
import { computed } from 'vue'

import EChart from '@/components/EChart.vue'
import {
  equityOption,
  histogramOption,
  holdingOption,
  perTradeOption,
  sideOption,
  winRateOption,
} from '@/components/records/chart-options'
import { chartPalette } from '@/lib/chart'
import type { ChartPalette } from '@/lib/chart'
import { moneyText, pctText, pnlText } from '@/lib/records'
import type { RecordAggregate } from '@/lib/records'
import { useReplayStore } from '@/stores/replay'

/**
 * 交易统计的六张图。
 *
 * 这一层只负责「哪张图放在哪、标题写什么」—— option 全部来自
 * `chart-options.ts` 的纯函数，配色来自 `lib/chart.ts`（源头是 CSS 变量）。
 *
 * 主题切换要重算：`palette` 里显式读了 `store.theme`，否则改的是 <html> 上的
 * data-theme 属性，computed 不知道要重算，图会一直是上一个主题的颜色。
 */
const props = defineProps<{
  agg: RecordAggregate
}>()

const store = useReplayStore()

const palette = computed<ChartPalette>(() => {
  void store.theme // 依赖主题：切换后重新读一遍 CSS 变量
  return chartPalette()
})

const equity = computed(() => equityOption(props.agg, palette.value))
const perTrade = computed(() => perTradeOption(props.agg, palette.value))
const winRate = computed(() => winRateOption(props.agg, palette.value))
const histogram = computed(() => histogramOption(props.agg, palette.value))
const holding = computed(() => holdingOption(props.agg, palette.value))
const side = computed(() => sideOption(props.agg, palette.value))

/** 多空两行摘要（卡片里那段文字，不是图）。胜率没有笔数时给 null，显示成「—」。 */
const sides = computed(() =>
  [
    { name: '多头', s: props.agg.long },
    { name: '空头', s: props.agg.short },
  ].map(({ name, s }) => ({
    name,
    count: s.count,
    pnl: s.pnl,
    avg: s.avg,
    winRate: s.count ? s.wins / s.count : null,
  })),
)

/** 赚红亏绿（国内习惯）；0 用中性色，别把「打平」说成赚钱。 */
function toneClass(n: number): string {
  if (n > 1e-6) return 'text-red-600 dark:text-red-400'
  if (n < -1e-6) return 'text-emerald-600 dark:text-emerald-400'
  return 'text-dim'
}

const card = 'min-w-0 rounded-lg border border-line bg-panel p-3'
const head = 'flex items-baseline gap-2'
const title = 'text-[14px] font-medium text-ink'
/** 每张图右上角都带一句「这张图在回答什么」，省得用户去猜 */
const hint = 'ml-auto shrink-0 text-[12px] text-faint'
</script>

<template>
  <div class="grid grid-cols-1 gap-3 xl:grid-cols-3">
    <!-- 1. 累计盈亏 + 回撤：宽图 -->
    <section :class="[card, 'xl:col-span-2']">
      <header :class="head">
        <h2 :class="title">累计盈亏与回撤</h2>
        <span :class="hint">
          最大回撤
          <b class="tnum text-emerald-600 dark:text-emerald-400">{{
            moneyText(agg.maxDrawdown)
          }}</b>
          <template v-if="agg.ddFrom > 0"> · 第 {{ agg.ddFrom }}→{{ agg.ddTo }} 笔</template>
        </span>
      </header>
      <EChart :option="equity" height="220px" class="mt-1" />
    </section>

    <!-- 2. 胜率环形 -->
    <section :class="card">
      <header :class="head">
        <h2 :class="title">胜率构成</h2>
        <span :class="hint">按已平仓笔数</span>
      </header>
      <EChart :option="winRate" height="220px" class="mt-1" />
    </section>

    <!-- 3. 每笔盈亏 + 累计 -->
    <section :class="[card, 'xl:col-span-2']">
      <header :class="head">
        <h2 :class="title">每笔盈亏与累计</h2>
        <span :class="hint">看曲线是被哪一笔打下来的</span>
      </header>
      <EChart :option="perTrade" height="200px" class="mt-1" />
    </section>

    <!-- 4. 盈亏分布 -->
    <section :class="card">
      <header :class="head">
        <h2 :class="title">盈亏分布</h2>
        <span :class="hint">形状比均值更重要</span>
      </header>
      <EChart :option="histogram" height="200px" class="mt-1" />
    </section>

    <!-- 5. 多空对比 -->
    <section :class="card">
      <header :class="head">
        <h2 :class="title">多空对比</h2>
        <span :class="hint">柱 = 累计盈亏，浅色柱 = 平均每笔</span>
      </header>
      <EChart :option="side" height="150px" class="mt-1" />
      <!--
        笔数 / 胜率放在文字里而不是柱顶：柱顶标签在「全负」的数据上会贴着 0 线，
        正好压在右上角的图例上。这张卡要读的是「两个方向差多少」，四行字比四个标签清楚。
      -->
      <dl class="mt-1 space-y-1 text-[12px]">
        <div v-for="s in sides" :key="s.name" class="flex items-baseline justify-between gap-2">
          <dt class="flex shrink-0 items-baseline gap-1.5">
            <span :class="toneClass(s.pnl)">{{ s.name }}</span>
            <span class="tnum text-faint">
              {{ s.count }} 笔 · 胜率 {{ s.winRate === null ? '—' : pctText(s.winRate, 0) }}
            </span>
          </dt>
          <dd class="tnum shrink-0">
            <span :class="toneClass(s.pnl)">{{ pnlText(s.pnl) }}</span>
            <span class="ml-1.5 text-faint">均 {{ pnlText(s.avg) }}</span>
          </dd>
        </div>
      </dl>
    </section>

    <!-- 6. 持仓根数 × 盈亏 -->
    <section :class="[card, 'xl:col-span-2']">
      <header :class="head">
        <h2 :class="title">持仓时长与盈亏</h2>
        <span :class="hint">点越大，这一笔的盈亏越大 · 平均持仓 {{ agg.avgBars }} 根</span>
      </header>
      <EChart :option="holding" height="200px" class="mt-1" />
    </section>
  </div>
</template>
