<script setup lang="ts">
import { computed } from 'vue'

import { drawdownLabel, pct, stageLabel, tiersLabel, usd } from '@/data/rules'
import type { Template } from '@/data/rules'

const props = defineProps<{ t: Template; nextName: string | null }>()
const emit = defineEmits<{ edit: []; remove: [] }>()

const rows = computed(() => {
  const r = props.t.rules
  return [
    { label: '盈利目标', value: usd(r.profitTarget) },
    { label: '最大回撤', value: usd(r.maxDrawdown) },
    { label: '回撤口径', value: drawdownLabel(r.drawdownType) },
    { label: '一致性上限', value: r.consistencyRatio == null ? '—' : `≤${pct(r.consistencyRatio)}` },
    {
      label: '盈利日',
      value:
        r.minProfitDays == null ? '—' : `${r.minProfitDays} 天 · 单日≥${usd(r.profitDayThreshold)}`,
    },
    { label: '冻结水位', value: usd(r.trailingLockLevel) },
    { label: '手数分档', value: tiersLabel(r.positionTiers) },
  ]
})
</script>

<template>
  <article
    class="flex w-[268px] shrink-0 snap-start flex-col rounded-xl border border-line bg-panel shadow-[0_1px_2px_rgba(15,23,42,0.04)]"
  >
    <header class="flex items-start justify-between gap-2 px-4 pt-4 pb-3">
      <h3 class="text-[15px] leading-tight font-semibold tracking-wide text-ink">{{ t.name }}</h3>
      <span
        class="shrink-0 rounded border border-line bg-elev px-1.5 py-0.5 text-[10px] tracking-wider text-dim"
        >{{ stageLabel(t.stage) }}</span
      >
    </header>

    <div class="space-y-2 border-t border-line px-4 py-3.5">
      <div v-for="row in rows" :key="row.label" class="flex items-baseline justify-between gap-3">
        <span class="text-[12px] text-faint">{{ row.label }}</span>
        <span class="tnum text-[12px] font-medium text-ink">{{ row.value }}</span>
      </div>
    </div>

    <!-- 参考图这里是价格区，模板没有价格字段，就放链信息 -->
    <div class="mx-4 mb-3 rounded-lg border border-line bg-elev px-3 py-2.5">
      <div class="flex items-baseline justify-between gap-3">
        <span class="text-[10px] tracking-wider text-faint uppercase">当前阶段</span>
        <span class="text-[12px] font-medium text-ink">{{ stageLabel(t.stage) }}</span>
      </div>
      <div class="mt-1.5 flex items-baseline justify-between gap-3">
        <span class="text-[10px] tracking-wider text-faint uppercase">达标后</span>
        <span class="truncate text-[12px] font-medium" :class="nextName ? 'text-accent-ink' : 'text-faint'">
          {{ nextName ?? '终点' }}
        </span>
      </div>
    </div>

    <div class="mt-auto space-y-1.5 px-4 pb-4">
      <button
        type="button"
        class="w-full rounded-md border border-accent/40 py-2 text-[13px] font-medium text-accent-ink transition-colors hover:bg-accent-soft"
        @click="emit('edit')"
      >
        编辑规则
      </button>
      <button
        type="button"
        class="w-full rounded-md border border-line py-2 text-[13px] text-faint transition-colors hover:border-line-strong hover:text-dim"
        @click="emit('remove')"
      >
        删除
      </button>
    </div>
  </article>
</template>
