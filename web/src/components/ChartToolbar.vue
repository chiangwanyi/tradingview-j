<script setup lang="ts">
import { computed } from 'vue'

import AnnotationTemplates from '@/components/AnnotationTemplates.vue'
import { TOOL_LABEL, isDrawTool, useReplayStore, type ChartTool, type DrawTool } from '@/stores/replay'

/**
 * 图表上方的操作条。
 *
 * 工具按钮只改本地工具状态；真正会改数据的动作（步进、接管、机器人）一律发消息给后端，
 * 由后端校验、执行、广播回来 —— 所以这里的按钮只是「请求」，不是「直接生效」。
 *
 * 工具分两组：**模式**（指针 / 修改）和**五种绘图工具**。
 *
 * 射线是唯一**单击即完成**的：点一下就是一条水平朝右的射线（预览跟着鼠标走），
 * 因为「水平线 + 价位」是复盘里最常用的一笔，两次单击纯属多余动作。
 * 其余四种是两次单击。绘图工具选中后，右边会跟出一排模板 ——
 * 那是这个工具自己的样式预设（各工具互不相干）。
 */

const store = useReplayStore()

const modeTools: ChartTool[] = ['select', 'modify']
const drawTools: DrawTool[] = ['trend', 'ray', 'rect', 'arrow', 'fib']

const modeHint: Record<string, string> = {
  select: '指针：只缩放/平移，不碰标注',
  modify: '修改：点选标注 → 拖端点改端点 / 拖线身整体移动 / Del 删除',
}

const drawHint: Record<DrawTool, string> = {
  trend: '趋势线：两次单击（起点 → 终点），按住 Shift 画水平线',
  ray: '射线：单击一下就画出一条水平朝右的射线；按住 Shift 改成两次单击画斜射线',
  rect: '矩形：两次单击定两个对角点',
  arrow: '箭头线：两次单击，终点带箭头；Shift 画水平箭头',
  fib: '斐波那契回撤：两次单击定价格区间，四条水平线 -1 / 0 / 1 / 2 自动排开',
}

const canEdit = computed(() => store.canOperate)
const operator = computed(() => store.operatorLabel)
/** 当前是不是绘图工具 —— 模板条只在绘图工具下出现（v-if 用得到，也顺手做类型收窄） */
const drawKind = computed<DrawTool | null>(() => (isDrawTool(store.tool) ? store.tool : null))

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
const btnActive = 'border-accent bg-accent-soft text-accent'
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
    <div class="flex items-center gap-1">
      <button
        v-for="t in modeTools"
        :key="t"
        type="button"
        :title="modeHint[t]"
        :class="[btn, store.tool === t ? btnActive : btnIdle]"
        @click="store.tool = t"
      >
        {{ TOOL_LABEL[t] }}
      </button>

      <span class="mx-1 h-4 w-px bg-line" />

      <button
        v-for="t in drawTools"
        :key="t"
        type="button"
        :title="drawHint[t]"
        :class="[btn, store.tool === t ? btnActive : btnIdle]"
        :disabled="!canEdit"
        @click="store.tool = t"
      >
        {{ TOOL_LABEL[t] }}
      </button>
    </div>

    <!-- 模板条：每个工具各有自己的一组（后端持久化，换实例也不会丢） -->
    <AnnotationTemplates v-if="drawKind" :kind="drawKind" />

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
      :class="[btn, 'border-amber-500 text-amber-600 hover:bg-amber-500/10 dark:text-amber-400']"
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
