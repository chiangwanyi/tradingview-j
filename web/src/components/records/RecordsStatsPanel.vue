<script setup lang="ts">
import { computed } from 'vue'

import RecordsCharts from '@/components/records/RecordsCharts.vue'
import RecordsKpi from '@/components/records/RecordsKpi.vue'
import TradeList from '@/components/records/TradeList.vue'
import { aggregate, hasTag, moneyText, tagUsage, tagTextColor } from '@/lib/records'
import type { TagUsage } from '@/lib/records'
import type { RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 统计页：当前所有交易记录的汇总 + 按标签筛选 + 筛选后的明细。
 *
 * 数据和视图的分工很硬：
 *
 *  - **标签筛选在这一层做**（`filtered`），KPI 和六张图都从筛完的这份算 ——
 *    否则会出现「图上是全部、列表是被筛过的」，用户会以为某一笔的钱凭空消失了。
 *  - 上层的**来源筛选**已经做过了（`records` 进来就是筛过的），这里不再重复判断，
 *    只把「这批里有哪些标签」算出来给筛选条用。
 *  - 标签是**同时满足**（多选 = 收窄），和后端 / 日历的口径完全一致。
 */
const props = defineProps<{
  /** 已经按来源筛过的记录 */
  records: RecordingSummary[]
  /** 全部记录里出现过的标签（打标签面板用，不受来源筛选影响） */
  library: TagUsage[]
  /** 选中的标签名（小写） */
  selectedTags: string[]
}>()

const emit = defineEmits<{
  'update:selectedTags': [string[]]
  replay: [RecordingSummary]
  remove: [string]
  tags: [RecordingSummary, RecordTag[]]
}>()

/** 这批记录里出现过的标签（用量按这批算，和筛选条上的数字对得上） */
const usage = computed(() => tagUsage(props.records))

/**
 * 筛选条上的标签 = 这批里出现过的 + 已选中但在这批里消失的。
 *
 * 后者不能省：用户先选了「追单」、再把来源切到手动，如果那条标签突然从条上
 * 消失，筛选就变成了一个看不见的开关 —— 界面上会显示「0 笔」而没有任何解释。
 */
const chips = computed<TagUsage[]>(() => {
  const out = [...usage.value]
  const have = new Set(out.map((u) => u.name.toLowerCase()))
  for (const key of props.selectedTags) {
    if (!have.has(key)) out.push({ name: key, color: '#64748b', count: 0 })
  }
  return out
})

const filtered = computed(() =>
  props.records.filter((r) => props.selectedTags.every((t) => hasTag(r, t))),
)
const agg = computed(() => aggregate(filtered.value))

function toggleTag(name: string): void {
  const key = name.toLowerCase()
  const next = props.selectedTags.includes(key)
    ? props.selectedTags.filter((t) => t !== key)
    : [...props.selectedTags, key]
  emit('update:selectedTags', next)
}

function clearTags(): void {
  emit('update:selectedTags', [])
}

const chip =
  'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[12px] transition'
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- ---------------- 标签筛选 ---------------- -->
    <section class="rounded-lg border border-line bg-panel px-3 py-2">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-[12px] text-faint">按标签</span>
        <button
          type="button"
          :class="[
            chip,
            selectedTags.length === 0
              ? 'border-ink bg-ink text-white'
              : 'border-line text-dim hover:border-accent hover:text-accent',
          ]"
          @click="clearTags"
        >
          全部标签
          <span class="tnum text-[11px] opacity-70">{{ records.length }}</span>
        </button>
        <button
          v-for="c in chips"
          :key="c.name"
          type="button"
          :class="[
            chip,
            selectedTags.includes(c.name.toLowerCase())
              ? 'border-ink/30 bg-ink/5 font-medium'
              : 'border-line hover:border-accent',
          ]"
          :style="{ color: tagTextColor(c.color) }"
          :title="c.count ? `${c.count} 笔带这个标签` : '当前来源下没有带这个标签的记录'"
          @click="toggleTag(c.name)"
        >
          <span class="h-1.5 w-1.5 rounded-full" :style="{ backgroundColor: c.color }" />
          {{ c.name }}
          <span class="tnum text-faint">{{ c.count }}</span>
        </button>
        <span v-if="chips.length === 0" class="text-[12px] text-faint">
          还没有标签 —— 在下面的明细里点「+ 标签」就能给某一笔打上
        </span>
        <span v-if="selectedTags.length" class="text-[11px] text-faint">
          （多选为「同时含」，共 {{ filtered.length }} 笔）
        </span>
      </div>
    </section>

    <RecordsKpi :agg="agg" scope="" />

    <RecordsCharts v-if="agg.count > 0" :agg="agg" />
    <section
      v-else
      class="rounded-lg border border-line bg-panel px-4 py-10 text-center text-[14px] text-dim"
    >
      没有符合当前来源 + 标签筛选的记录。
      <button
        v-if="selectedTags.length"
        type="button"
        class="text-accent hover:underline"
        @click="clearTags"
      >
        清除标签筛选
      </button>
    </section>

    <!-- ---------------- 明细 ---------------- -->
    <section class="rounded-lg border border-line bg-panel">
      <header class="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
        <h2 class="text-[13px] font-medium text-ink">交易明细</h2>
        <span class="text-[11px] text-faint">
          {{ filtered.length }} 笔 · 共 {{ agg.totalSteps }} 步操作 · 点任意一行回到画布逐格看这一笔
        </span>
        <span
          v-if="agg.bySymbol.length > 1"
          class="ml-auto flex flex-wrap items-center gap-2 text-[11px] text-faint"
        >
          <span v-for="s in agg.bySymbol" :key="s.symbol" class="tnum">
            {{ s.symbol }}
            <b :class="s.pnl > 1e-6 ? 'text-up' : s.pnl < -1e-6 ? 'text-down' : 'text-dim'">
              {{ moneyText(s.pnl) }}
            </b>
            <span class="text-faint">({{ s.count }} 笔)</span>
          </span>
        </span>
      </header>

      <TradeList
        :rows="agg.rows"
        :library="library"
        :empty-text="
          selectedTags.length ? '没有同时带这些标签的记录' : '当前来源下还没有交易记录'
        "
        @replay="(rec) => emit('replay', rec)"
        @remove="(id) => emit('remove', id)"
        @tags="(rec, tags) => emit('tags', rec, tags)"
      />
    </section>

    <!--
      统计口径：这一页每个数字怎么来的，写在能用肉眼看到的地方。
      口径只该有一份解释 —— 后端算一笔（trade.go），这里只解释怎么汇总。
    -->
    <details class="rounded-lg border border-line bg-panel px-3 py-2 text-[12px] leading-5 text-dim">
      <summary class="cursor-pointer select-none text-[13px] font-medium text-ink">
        统计口径
      </summary>
      <ul class="mt-2 grid list-disc gap-x-6 gap-y-1 pl-4 md:grid-cols-2">
        <li>
          <b class="text-ink">一笔 = 一次完整仓位</b
          >：从空仓变成有仓开始记，回到空仓时收尾。加减仓不另记一笔。
        </li>
        <li>
          <b class="text-ink">日历 / 按天统计</b>：按<b>记录生成时间</b>归日（开仓那一刻），
          不是行情走到的那一天 —— 今天补做去年的行情，五笔都算今天。
        </li>
        <li>
          <b class="text-ink">盈亏</b
          >：后端的已实现盈亏（成交撮合算出来的钱），这一页只求和，不重算。
        </li>
        <li><b class="text-ink">胜率</b> = 盈利笔数 ÷ <b>已平仓</b>笔数。还在持仓中的不计入分母。</li>
        <li><b class="text-ink">盈亏比</b> = 总盈利 ÷ |总亏损|。没有亏损单时显示 ∞。</li>
        <li><b class="text-ink">期望</b> = 累计盈亏 ÷ 笔数，就是「随便抽一笔平均赚多少」。</li>
        <li>
          <b class="text-ink">最大回撤</b>：按记录时间（这笔是什么时候做的）排序后，累计曲线上
          「峰值 → 之后最低点」的最大落差。
        </li>
        <li>
          <b class="text-ink">MFE / MAE</b>：持仓期间最大有利 /
          最大不利波动（后端按每根的最高最低算）。
        </li>
        <li>
          <b class="text-ink">来源 / 标签</b>：来源在开仓那一刻定下（机器人按策略细分），
          标签是你自己打的记号，两者都随记录存盘，重启后还在。
        </li>
      </ul>
    </details>
  </div>
</template>
