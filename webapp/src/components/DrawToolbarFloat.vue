<script setup lang="ts">
import { ref } from 'vue'

import { DRAW_TOOLS, TOOL_LABEL, useTradeStore, type DrawTool } from '@/stores/trade'

/**
 * 画布上的绘图形工具浮窗。
 *
 * 从工具条里搬出来的五个按钮（趋势 / 射线 / 矩形 / 箭头 / 斐波），理由只有一个：
 * 它们本来就贴在 K 线上用，留在顶部那条操作栏里既占位置、又要跨半屏去点。
 * 拖动位置只活在这个标签页里，不存档（和工具栏其它偏好不同 —— 没人在意它记不记得）。
 */
const store = useTradeStore()

const drawHint: Record<DrawTool, string> = {
  trend: '趋势线：两次单击（起点 → 终点），按住 Shift 画水平线',
  ray: '射线：单击一下就画出一条水平朝右的射线；按住 Shift 改成两次单击画斜射线',
  rect: '矩形：两次单击定两个对角点',
  arrow: '箭头线：两次单击，终点带箭头；Shift 画水平箭头',
  fib: '斐波那契回撤：两次单击定价格区间，四条水平线 -1 / 0 / 1 / 2 自动排开',
}

const box = ref<HTMLElement | null>(null)
/** 拖动过之后的像素位置；null = 还在默认的「画布底部居中」 */
const pos = ref<{ x: number; y: number } | null>(null)
let grab: { dx: number; dy: number } | null = null

const btn = 'rounded border border-line px-2 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
const btnActive = 'border-accent bg-accent-soft text-accent'

function startDrag(e: PointerEvent): void {
  const el = box.value
  const parent = el?.offsetParent as HTMLElement | null
  if (!el || !parent) return
  const r = el.getBoundingClientRect()
  const p = parent.getBoundingClientRect()
  // 先落成像素再拖：默认是 50% 居中，直接从那里算偏移会跳一下
  pos.value = { x: r.left - p.left, y: r.top - p.top }
  grab = { dx: e.clientX - r.left, dy: e.clientY - r.top }
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onDrag(e: PointerEvent): void {
  const el = box.value
  const parent = el?.offsetParent as HTMLElement | null
  if (!grab || !pos.value || !el || !parent) return
  const p = parent.getBoundingClientRect()
  const clamp = (v: number, max: number): number => Math.min(Math.max(v, 0), Math.max(max, 0))
  pos.value = {
    x: clamp(e.clientX - p.left - grab.dx, parent.clientWidth - el.offsetWidth),
    y: clamp(e.clientY - p.top - grab.dy, parent.clientHeight - el.offsetHeight),
  }
}

function endDrag(): void {
  grab = null
}
</script>

<template>
  <div
    ref="box"
    class="absolute z-30 flex items-center gap-1 rounded-lg border border-line bg-panel/95 px-1.5 py-1 shadow-lg"
    :style="
      pos
        ? { left: `${pos.x}px`, top: `${pos.y}px` }
        : { left: '50%', bottom: '12px', transform: 'translateX(-50%)' }
    "
  >
    <span
      class="cursor-move touch-none px-1 text-[11px] leading-none text-faint select-none"
      title="按住拖动这个工具浮窗"
      @pointerdown="startDrag"
      @pointermove="onDrag"
      @pointerup="endDrag"
      @pointercancel="endDrag"
      >⠿</span
    >

    <button
      v-for="t in DRAW_TOOLS"
      :key="t"
      type="button"
      :title="drawHint[t]"
      :class="[btn, store.tool === t ? btnActive : btnIdle]"
      :disabled="!store.canOperate"
      @click="store.tool = store.tool === t ? 'select' : t"
    >
      {{ TOOL_LABEL[t] }}
    </button>
  </div>
</template>
