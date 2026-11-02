<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'

import AnnotationTemplates from '@/components/AnnotationTemplates.vue'
import { DRAW_TOOLS, TOOL_LABEL, isDrawTool, useTradeStore, type DrawTool } from '@/stores/trade'

/**
 * 画布上的**工具浮窗**：五种绘图工具 + 【下一根】。
 *
 * 为什么从顶部工具条搬下来：它们都是「对着 K 线用」的动作，跨半屏去点没道理；
 * 而且浮窗可以拖到顺手的位置（位置存浏览器，刷新还在）。
 *
 * 模板条（`AnnotationTemplates`）挂在浮窗**下面**，只在手里拿着笔时出现：
 * 它属于「这条线用什么样式」，和选工具是同一步动作。平时不占地方。
 */
const store = useTradeStore()

const drawHint: Record<DrawTool, string> = {
  trend: '趋势线：两次单击（起点 → 终点），按住 Shift 画水平线',
  ray: '射线：单击一下就画出一条水平朝右的射线；按住 Shift 改成两次单击画斜射线',
  rect: '矩形：两次单击定两个对角点',
  arrow: '箭头线：两次单击，终点带箭头；Shift 画水平箭头',
  fib: '斐波那契回撤：两次单击定价格区间，四条水平线 -1 / 0 / 1 / 2 自动排开',
}

/** 当前是不是绘图工具（决定模板条出不出现，也顺手做类型收窄） */
const drawKind = computed<DrawTool | null>(() => (isDrawTool(store.tool) ? store.tool : null))

// ---------------- 位置（本机存档） ----------------

const POS_KEY = 'tv-draw-toolbar-pos-v1'
/**
 * 默认位置：画布顶边、偏右一点。
 *
 * 不贴左边是因为画布左上角有提示条（`ChartPanel` 里那条 amber 的 `top-2 left-2`），
 * 贴上会互相遮；不贴底边是因为模板条挂在浮窗**下面**，贴着底边展开就出画布了。
 */
const DEFAULT_POS = { x: 320, y: 8 }

const box = ref<HTMLElement | null>(null)
const pos = ref(readSavedPos() ?? { ...DEFAULT_POS })
let grab: { dx: number; dy: number } | null = null

function readSavedPos(): { x: number; y: number } | null {
  try {
    const v = JSON.parse(localStorage.getItem(POS_KEY) ?? 'null') as { x?: unknown; y?: unknown } | null
    if (!v || typeof v.x !== 'number' || typeof v.y !== 'number') return null
    if (!Number.isFinite(v.x) || !Number.isFinite(v.y)) return null
    return { x: v.x, y: v.y }
  } catch {
    return null
  }
}

function savePos(): void {
  try {
    localStorage.setItem(
      POS_KEY,
      JSON.stringify({ x: Math.round(pos.value.x), y: Math.round(pos.value.y) }),
    )
  } catch {
    // 隐私模式下 localStorage 会直接抛：记不住位置不影响这次使用
  }
}

/** 父节点（画布那一块）里能放到的最大左上角，别让浮窗被拖到看不见的地方 */
function clampToParent(x: number, y: number): { x: number; y: number } {
  const el = box.value
  const parent = el?.offsetParent as HTMLElement | null
  if (!el || !parent) return { x, y }
  const clamp = (v: number, max: number): number => Math.min(Math.max(v, 0), Math.max(max, 0))
  return {
    x: clamp(x, parent.clientWidth - el.offsetWidth),
    y: clamp(y, parent.clientHeight - el.offsetHeight),
  }
}

/** 存档里的位置可能是「收面板时」记下的：画布现在矮了，先拉回可见区 */
onMounted(() => {
  void nextTick(() => {
    pos.value = clampToParent(pos.value.x, pos.value.y)
  })
})

function startDrag(e: PointerEvent): void {
  const el = box.value
  if (!el) return
  const r = el.getBoundingClientRect()
  grab = { dx: e.clientX - r.left, dy: e.clientY - r.top }
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onDrag(e: PointerEvent): void {
  const el = box.value
  const parent = el?.offsetParent as HTMLElement | null
  if (!grab || !el || !parent) return
  const p = parent.getBoundingClientRect()
  pos.value = clampToParent(e.clientX - p.left - grab.dx, e.clientY - p.top - grab.dy)
}

function endDrag(): void {
  if (!grab) return
  grab = null
  savePos()
}

// ---------------- 【下一根】 ----------------

/**
 * 四个条件缺一不可：有盘面、后端说磁盘上还有下一根、没有一条 step 正在路上（防连点）、
 * 连接是通的。**连接那一条最重要** —— 断线时按钮必须立刻变灰，否则人按下去什么都不会发生，
 * 只会以为是程序卡了。
 */
const canStep = computed(
  () => !!store.instance && store.hasNext && !store.pendingStep && store.socketState === 'open',
)

const stepHint = computed(() => {
  if (store.socketState !== 'open') return '和交易服务的连接断了，正在重连：连上之后才能往后放'
  if (!store.instance) return '盘面还没加载出来'
  if (store.pendingStep) return '这一根正在从后端取回来…'
  if (!store.hasNext) {
    return `${store.currentTime || '当前这一根'} 已经是磁盘上最后一根：等抓取程序再录一段行情`
  }
  return '把盘面往后放一根（等同画布上按 →）。这一根由后端读 CSV 算出来、写进操作记录，再把结果广播回来'
})

const btn = 'rounded border border-line px-2 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
const btnActive = 'border-accent bg-accent-soft text-accent'
</script>

<template>
  <!-- z-[46]：压在【下单】浮窗（z-45）上面 —— 万一被拖过去叠住了，也得看得见、拖得回来 -->
  <div
    ref="box"
    class="absolute z-[46] flex items-center gap-1 rounded-lg border border-line bg-panel/95 px-1.5 py-1 shadow-lg"
    :style="{ left: `${pos.x}px`, top: `${pos.y}px` }"
  >
    <span
      class="cursor-move touch-none px-1 text-[11px] leading-none text-faint select-none"
      title="按住拖动这个工具浮窗（位置会记住）"
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

    <!-- 竖线 + 左边距：绘图工具是「画」，【下一根】是「翻页」，两件事别挤成一组 -->
    <div class="mx-1.5 h-4 w-px bg-line" />

    <button
      type="button"
      :class="[btn, store.pendingStep ? btnActive : btnIdle, 'tnum']"
      :disabled="!canStep"
      :title="stepHint"
      @click="store.step()"
    >
      {{ store.pendingStep ? '取下一根…' : '下一根 ›' }}
    </button>

    <!--
      模板条：只在拿着笔的时候出现，贴在浮窗下面。
      `w-max` 是必须的 —— 浮窗本身只有三百来像素宽，绝对定位默认会缩到那么宽，
      模板 chip 会被压成一行一个。上限用视口宽兜住，别铺出屏幕。
    -->
    <div
      v-if="drawKind"
      class="absolute top-full left-0 mt-1 w-max max-w-[70vw] rounded-lg border border-line bg-panel px-2 py-1.5 shadow-lg"
    >
      <AnnotationTemplates :kind="drawKind" />
    </div>
  </div>
</template>
