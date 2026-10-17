<script setup lang="ts">
import { computed } from 'vue'

import { useReplayStore } from '@/stores/replay'
import { ANNOTATION_COLORS } from '@/types/protocol'
import type { ChartTool } from '@/stores/replay'

/**
 * 图表上方的操作条。
 *
 * 工具按钮只改本地工具状态；真正会改数据的动作（步进、接管、机器人）一律发消息给后端，
 * 由后端校验、执行、广播回来 —— 所以这里的按钮只是「请求」，不是「直接生效」。
 */

const store = useReplayStore()

const tools: Array<{ key: ChartTool; label: string; hint: string }> = [
  { key: 'select', label: '选择', hint: '点选/拖动已有标注，空白处拖动平移' },
  { key: 'line', label: '线段', hint: '拖出一段线（严格吸附到起止 K 线）' },
  { key: 'rect', label: '矩形', hint: '拖出一个矩形区域' },
  { key: 'text', label: '文字', hint: '点一下 K 线，输入文字' },
]

const canEdit = computed(() => store.canOperate)
const operator = computed(() => store.operatorLabel)
const canStartBot = computed(() => {
  const inst = store.instance
  if (!inst || inst.bot) return false
  return inst.operator === '' || store.isOperator
})
const hasMoreHint = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  if (inst.hasMore) return `已加载 ${inst.bars.length + inst.hiddenBars} 根，还剩 ${inst.hiddenBars} 根未揭示`
  return `已加载 ${inst.bars.length + inst.hiddenBars} 根，数据目录里没有更晚的了`
})

const btn = 'rounded border border-line px-2 py-1 text-[12px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
    <div class="flex items-center gap-1">
      <button
        v-for="t in tools"
        :key="t.key"
        type="button"
        :title="t.hint"
        :class="[
          btn,
          store.tool === t.key
            ? 'border-accent bg-accent-soft text-accent'
            : btnIdle,
        ]"
        :disabled="!canEdit && t.key !== 'select'"
        @click="store.tool = t.key"
      >
        {{ t.label }}
      </button>
    </div>

    <div class="flex items-center gap-1 pl-1">
      <button
        v-for="c in ANNOTATION_COLORS"
        :key="c"
        type="button"
        class="h-4 w-4 rounded-full border"
        :class="store.color === c ? 'border-ink' : 'border-line'"
        :style="{ backgroundColor: c }"
        :title="`标注颜色 ${c}`"
        @click="store.color = c"
      />
    </div>

    <span class="text-[11px] text-faint">Ctrl 吸附 OHLC</span>

    <div class="h-4 w-px bg-line" />

    <button
      type="button"
      :class="[btn, store.showEma ? 'border-accent text-accent' : btnIdle]"
      title="显示/隐藏 EMA20（指标值由后端算好，这里只是开关）"
      @click="store.showEma = !store.showEma"
    >
      EMA20
    </button>

    <div class="h-4 w-px bg-line" />

    <button
      type="button"
      class="rounded bg-emerald-600 px-3 py-1 text-[12px] font-medium text-white hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="!canEdit || !store.instance || (store.instance?.bars.length ?? 0) === 0"
      title="揭示下一根 K 线（会按这根的最高/最低价撮合挂单）"
      @click="store.step()"
    >
      下一根 →
    </button>

    <button type="button" :class="[btn, btnIdle]" :disabled="!store.isOperator" @click="store.releaseControl()">
      交还操作权
    </button>
    <button
      type="button"
      :class="[
        btn,
        'border-amber-500 text-amber-600 hover:bg-amber-500/10 dark:text-amber-400',
      ]"
      :disabled="store.isOperator || !store.instance || (store.instance?.operator ?? '') !== ''"
      @click="store.takeControl()"
    >
      接管
    </button>

    <button
      v-if="!store.botRunning"
      type="button"
      :class="[btn, 'border-sky-600 text-sky-600 hover:bg-sky-600/10 dark:text-sky-400']"
      :disabled="!canStartBot"
      title="机器人会接管操作权，自动步进/画标注/下单（用于演示与联调）"
      @click="store.startBot()"
    >
      启动机器人
    </button>
    <button
      v-else
      type="button"
      class="rounded bg-sky-600 px-2 py-1 text-[12px] text-white hover:bg-sky-500"
      @click="store.stopBot()"
    >
      停止机器人
    </button>

    <div class="ml-auto flex items-center gap-3 text-[11px] text-dim">
      <span v-if="store.instance">
        操作权：<b :class="store.isOperator ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
          {{ operator || '无人' }}
        </b>
      </span>
      <span v-if="store.instance" class="text-faint">{{ hasMoreHint }}</span>
    </div>
  </div>
</template>
