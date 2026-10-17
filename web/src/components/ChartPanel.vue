<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { drawChart, themeOf, type HoverState } from '@/components/chart/render'
import { ChartScale, TIME_AXIS_HEIGHT, formatAxisTime, formatPrice, snapToTick } from '@/components/chart/scale'
import { closedTrades } from '@/components/chart/trades'
import { useReplayStore } from '@/stores/replay'
import type { Bar, OrderKind, Point, Side } from '@/types/protocol'

/**
 * 画布 = Canvas（K 线/指标/挂单价线/十字光标）+ SVG（标注图形）。
 *
 * 为什么要分两层：
 *   - K 线一次几百根、每帧重画，Canvas 最快；
 *   - 标注要能被指针命中、能拖、能选中，SVG 元素天然有 hit-test，不用自己写拾取算法。
 *
 * 「前端预览、后端定稿」的做法（需求第 10 条）：
 *   拖动/绘制过程中只更新本地 draft（虚线幽灵图形），松手时把**时间+价格**发往后端；
 *   后端校验、吸附、编号后再广播回来，界面上的正式图形永远来自后端广播。
 *   所以即使后端拒绝了（比如画到未来的 K 线上），幽灵图形一消失，界面上什么都不会留下。
 */

const store = useReplayStore()

/** 画布配色跟着主题走：两套配色都在 render.ts 里，这里只选一套 */
const theme = computed(() => themeOf(store.theme))
/** 标注文字块的底色：深色用半透明深底，浅色用半透明白底 */
const labelBg = computed(() => (theme.value.mode === 'light' ? 'rgba(255, 255, 255, 0.86)' : 'rgba(11, 17, 32, 0.72)'))

const host = ref<HTMLDivElement | null>(null)
const canvas = ref<HTMLCanvasElement | null>(null)
const scale = new ChartScale()

const size = ref({ width: 0, height: 0 })
/** 视图变化（缩放/平移/尺寸）时自增，用来让依赖坐标的 computed 重新计算 */
const viewTick = ref(0)
const hover = ref<HoverState | null>(null)
const ctrlDown = ref(false)

/**
 * 右键小菜单：位置（画布内的本地像素）+ **已经吸附到最小跳动**的价格。
 *
 * 菜单只提供「按这个价格挂单 / 复制这个价格」，所以价格必须在打开那一刻就定死，
 * 不能等点击时再从鼠标位置算一遍 —— 手一抖价格就变了。
 */
const menu = ref<{ x: number; y: number; price: number } | null>(null)
/** 复制价格/挂单成功后的一句轻提示（1.4 秒自己消失） */
const toast = ref('')
let toastTimer = 0

interface ChartPoint {
  time: string
  price: number
}

interface DraftState {
  kind: 'line' | 'rect' | 'text'
  start: ChartPoint
  end: ChartPoint
  snap: boolean
}

interface MovingState {
  id: string
  kind: string
  text: string
  color: string
  start: ChartPoint
  end: ChartPoint
  snap: boolean
}

const draft = ref<DraftState | null>(null)
const moving = ref<MovingState | null>(null)
/** 上一次拖动是否真的移动过：拖完松手会补一个 click，别让它把选中状态点没了 */
let lastDragMoved = false

// ---------------- 尺寸与重绘 ----------------

let rafId = 0
let observer: ResizeObserver | null = null

function requestDraw(): void {
  viewTick.value += 1
  if (rafId) return
  rafId = requestAnimationFrame(() => {
    rafId = 0
    draw()
  })
}

function draw(): void {
  const el = canvas.value
  const box = host.value
  if (!el || !box) return
  const dpr = window.devicePixelRatio || 1
  const w = Math.max(1, box.clientWidth)
  const h = Math.max(1, box.clientHeight)
  if (el.width !== Math.round(w * dpr) || el.height !== Math.round(h * dpr)) {
    el.width = Math.round(w * dpr)
    el.height = Math.round(h * dpr)
  }
  if (size.value.width !== w || size.value.height !== h) {
    size.value = { width: w, height: h }
    scale.setSize(w, h)
    scale.clampScroll()
    scale.fitPrice(true)
  }
  const ctx = el.getContext('2d')
  if (!ctx) return
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  const inst = store.instance
  const trades = inst?.trades ?? []
  drawChart({
    ctx,
    scale,
    theme: theme.value,
    hover: hover.value,
    symbol: inst?.symbol ?? '',
    orders: inst?.orders ?? [],
    trades,
    closed: closedTrades(trades),
    position: inst?.position ?? { quantity: 0, side: 'flat', averagePrice: 0 },
    realizedPnl: inst?.realizedPnl ?? 0,
    showEma: store.showEma,
    tick: store.tick,
  })
}

onMounted(() => {
  scale.setBars(store.bars)
  scale.setSize(host.value?.clientWidth ?? 0, host.value?.clientHeight ?? 0)
  scale.resetView()
  requestDraw()

  observer = new ResizeObserver(() => {
    scale.setSize(host.value?.clientWidth ?? 0, host.value?.clientHeight ?? 0)
    if (scale.autoPrice) scale.fitPrice(true)
    requestDraw()
  })
  if (host.value) observer.observe(host.value)
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('keyup', onKeyUp)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('keyup', onKeyUp)
  window.removeEventListener('pointermove', onWindowPointerMove)
  window.removeEventListener('pointerup', onWindowPointerUp)
  if (rafId) cancelAnimationFrame(rafId)
})

// 实例换了：重置视图（否则会拿着上一个标的的缩放去画新标的）
watch(
  () => store.instance?.id,
  () => {
    scale.setBars(store.bars)
    scale.resetView()
    hover.value = null
    draft.value = null
    moving.value = null
    requestDraw()
  },
)

// 新增一根 K 线：跟着走，别让用户每次手动拖
watch(
  () => store.bars.length,
  (next, prev) => {
    scale.setBars(store.bars)
    if (next > prev) {
      scale.scrollToTail()
      scale.fitPrice(true)
    }
    requestDraw()
  },
)

watch(
  () => store.instance?.orders,
  () => requestDraw(),
)

// 成交/平仓会改变画布上的三角与连线，也要立刻重画
watch(
  () => store.instance?.trades,
  () => requestDraw(),
)
watch(
  () => store.instance?.position,
  () => requestDraw(),
)
watch(
  () => store.instance?.realizedPnl,
  () => requestDraw(),
)

// 换主题要立刻重画（Canvas 的像素不会跟着 CSS 变量变）
watch(() => store.theme, () => requestDraw())
watch(() => store.showEma, () => requestDraw())

// ---------------- 坐标换算 ----------------

/** 指针位置 -> 数据坐标（最近的 K 线时间 + 价格）。Ctrl 按下时价格吸附到 OHLC。 */
function pointAt(x: number, y: number, snap: boolean): ChartPoint | null {
  if (scale.bars.length === 0) return null
  const idx = scale.nearestIndex(x)
  const bar = scale.bars[idx]
  if (!bar) return null
  const price = snap ? nearestOHLC(bar, scale.priceAtY(y)) : scale.priceAtY(y)
  return { time: bar.time, price }
}

/** 和后端 nearestOHLC 同样的取舍（O 优先，严格小于才替换），保证预览与定稿一致。 */
function nearestOHLC(bar: Bar, price: number): number {
  let best = bar.open
  let diff = Math.abs(bar.open - price)
  for (const v of [bar.high, bar.low, bar.close]) {
    const d = Math.abs(v - price)
    if (d < diff) {
      best = v
      diff = d
    }
  }
  return best
}

function toScreen(p: ChartPoint): { x: number; y: number } | null {
  const idx = scale.indexOfTime(p.time)
  if (idx === undefined) return null
  return { x: scale.xOf(idx), y: scale.yOf(p.price) }
}

// ---------------- 指针交互 ----------------

interface DragState {
  mode: 'none' | 'pan' | 'draw' | 'move'
  startX: number
  startY: number
  lastX: number
  lastY: number
  moved: boolean
  // 绘制
  snap: boolean
  // 移动
  moveId: string
  moveKind: string
  moveText: string
  moveColor: string
  baseStart: Point
  baseEnd: Point
  baseIndex: number
  baseIndexEnd: number
  basePrice: number
  basePriceEnd: number
  /** 指针按下那一刻的价格：拖动时的位移基准（不能用标注自己的价格，否则一按就跳） */
  downPrice: number
}

function emptyDrag(): DragState {
  return {
    mode: 'none',
    startX: 0,
    startY: 0,
    lastX: 0,
    lastY: 0,
    moved: false,
    snap: false,
    moveId: '',
    moveKind: '',
    moveText: '',
    moveColor: '',
    baseStart: { time: '', price: 0 },
    baseEnd: { time: '', price: 0 },
    baseIndex: 0,
    baseIndexEnd: 0,
    basePrice: 0,
    basePriceEnd: 0,
    downPrice: 0,
  }
}

let drag: DragState = emptyDrag()

function localPoint(ev: { clientX: number; clientY: number }): { x: number; y: number } | null {
  const box = host.value
  if (!box) return null
  const rect = box.getBoundingClientRect()
  return { x: ev.clientX - rect.left, y: ev.clientY - rect.top }
}

function onPointerDown(ev: PointerEvent): void {
  menu.value = null
  if (!store.instance) return
  const p = localPoint(ev)
  if (!p) return

  // 中键：任何工具下都平移
  if (ev.button === 1) {
    ev.preventDefault()
    drag = { ...emptyDrag(), mode: 'pan', startX: p.x, startY: p.y, lastX: p.x, lastY: p.y }
    beginPointerCapture()
    return
  }
  if (ev.button !== 0) return

  const tool = store.tool
  if (tool === 'select') {
    drag = { ...emptyDrag(), mode: 'pan', startX: p.x, startY: p.y, lastX: p.x, lastY: p.y, snap: ev.ctrlKey }
    beginPointerCapture()
    return
  }

  const start = pointAt(p.x, p.y, ev.ctrlKey)
  if (!start) return
  if (tool === 'text') {
    // 文字：单击即定位，随即弹输入框（第一版用 prompt —— 够用，且不会在画布上叠一层输入框）
    finishText({ kind: 'text', start, end: start, snap: ev.ctrlKey }, ev.ctrlKey)
    return
  }
  drag = {
    ...emptyDrag(),
    mode: 'draw',
    startX: p.x,
    startY: p.y,
    lastX: p.x,
    lastY: p.y,
    snap: ev.ctrlKey,
  }
  draft.value = { kind: tool, start, end: start, snap: ev.ctrlKey }
  beginPointerCapture()
}

function beginPointerCapture(): void {
  window.addEventListener('pointermove', onWindowPointerMove)
  window.addEventListener('pointerup', onWindowPointerUp)
}

function endPointerCapture(): void {
  window.removeEventListener('pointermove', onWindowPointerMove)
  window.removeEventListener('pointerup', onWindowPointerUp)
}

function onWindowPointerMove(ev: PointerEvent): void {
  const p = localPoint(ev)
  if (!p) return
  ctrlDown.value = ev.ctrlKey

  if (drag.mode === 'pan') {
    const dx = p.x - drag.lastX
    const dy = p.y - drag.lastY
    drag.lastX = p.x
    drag.lastY = p.y
    if (Math.abs(p.x - drag.startX) + Math.abs(p.y - drag.startY) > 2) drag.moved = true
    scale.panBy(dx, dy)
    requestDraw()
    return
  }

  if (drag.mode === 'draw' && draft.value) {
    const next = pointAt(p.x, p.y, ev.ctrlKey)
    if (next) {
      draft.value = { ...draft.value, end: next, snap: ev.ctrlKey }
      drag.snap = ev.ctrlKey
      drag.moved = true
      requestDraw()
    }
    return
  }

  if (drag.mode === 'move' && moving.value) {
    drag.snap = ev.ctrlKey
    const deltaBars = Math.round((p.x - drag.startX) / scale.barWidth)
    const deltaPrice = scale.priceAtY(p.y) - drag.downPrice
    const bars = scale.bars
    const maxIdx = Math.max(0, bars.length - 1)
    const idxStart = Math.min(maxIdx, Math.max(0, drag.baseIndex + deltaBars))
    const idxEnd = Math.min(maxIdx, Math.max(0, drag.baseIndexEnd + deltaBars))
    const barStart = bars[idxStart]
    const barEnd = bars[idxEnd]
    if (!barStart || !barEnd) return
    const priceStart = drag.snap ? nearestOHLC(barStart, drag.basePrice + deltaPrice) : drag.basePrice + deltaPrice
    const priceEnd = drag.snap ? nearestOHLC(barEnd, drag.basePriceEnd + deltaPrice) : drag.basePriceEnd + deltaPrice
    moving.value = {
      ...moving.value,
      start: { time: barStart.time, price: priceStart },
      end: { time: barEnd.time, price: drag.moveKind === 'text' ? priceStart : priceEnd },
    }
    drag.moved = true
    requestDraw()
  }
}

function onWindowPointerUp(ev: PointerEvent): void {
  const p = localPoint(ev)
  if (drag.mode === 'draw' && draft.value && p) {
    const d = draft.value
    if (d.kind === 'text') {
      finishText(d, ev.ctrlKey)
    } else if (drag.moved) {
      // 只在真的拖出了距离时才提交：单击不该产生一个零长度的图形
      commitDraft(d, ev.ctrlKey)
    }
    draft.value = null
  } else if (drag.mode === 'move' && moving.value && drag.moved) {
    lastDragMoved = true
    const m = moving.value
    store.moveAnnotation(
      m.id,
      { time: m.start.time, price: round(m.start.price) },
      m.kind === 'text' ? undefined : { time: m.end.time, price: round(m.end.price) },
      drag.snap,
    )
  }
  moving.value = null
  drag = emptyDrag()
  endPointerCapture()
  requestDraw()
}

function onPointerMove(ev: PointerEvent): void {
  if (!store.instance) return
  const p = localPoint(ev)
  if (!p) return
  ctrlDown.value = ev.ctrlKey
  if (drag.mode !== 'none') return
  const idx = scale.nearestIndex(p.x)
  hover.value = { index: idx, x: p.x, y: p.y, inside: true }
  requestDraw()
}

function onPointerLeave(): void {
  hover.value = null
  ctrlDown.value = false
  requestDraw()
}

function onWheel(ev: WheelEvent): void {
  menu.value = null
  if (!store.instance) return
  ev.preventDefault()
  const p = localPoint(ev)
  if (!p) return
  const factor = ev.deltaY > 0 ? 1 / 1.12 : 1.12
  if (ev.shiftKey) {
    scale.zoomPrice(factor, p.y)
  } else {
    scale.zoomTime(factor, p.x)
  }
  requestDraw()
}

function onDoubleClick(): void {
  scale.resetPrice()
  requestDraw()
}

// ---------------- 右键小菜单 ----------------

/** 菜单里那句价格：三位小数（BTCUSDT 0.1 一跳）也不会被截成两位 */
const menuPriceText = computed(() => (menu.value ? formatPrice(menu.value.price, store.priceDecimals) : ''))

/** 靠边右键时把菜单挪回画布内，别一半露在窗口外面 */
const menuStyle = computed(() => {
  const m = menu.value
  if (!m) return {}
  const w = 226
  const h = 112
  return {
    left: `${Math.max(4, Math.min(m.x, size.value.width - w - 4))}px`,
    top: `${Math.max(4, Math.min(m.y, size.value.height - h - 4))}px`,
    width: `${w}px`,
  }
})

function onContextMenu(ev: MouseEvent): void {
  ev.preventDefault()
  if (!store.instance) return
  const p = localPoint(ev)
  if (!p) return
  menu.value = { x: p.x, y: p.y, price: snapToTick(scale.priceAtY(p.y), store.tick) }
}

function showToast(text: string): void {
  toast.value = text
  if (toastTimer) window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => {
    toast.value = ''
  }, 1400)
}

function menuOrder(side: Side, kind: OrderKind): void {
  const m = menu.value
  if (!m) return
  if (!store.canOperate) {
    showToast('现在只能看：先「接管」操作权再下单')
    menu.value = null
    return
  }
  store.placeOrder(side, kind, m.price, store.orderQuantity)
  menu.value = null
  showToast(`已挂 ${side === 'buy' ? '买入' : '卖出'}${kind === 'limit' ? '限价' : '止损'} ${formatPrice(m.price, store.priceDecimals)}`)
}

async function menuCopy(): Promise<void> {
  const m = menu.value
  if (!m) return
  const text = formatPrice(m.price, store.priceDecimals)
  const ok = await copyText(text)
  menu.value = null
  showToast(ok ? `已复制 ${text}` : '复制失败：浏览器不允许访问剪贴板')
}

/** 剪贴板：优先 Clipboard API，非安全上下文（http + 非 localhost）退回老办法 */
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 落到下面的兜底
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

onBeforeUnmount(() => {
  if (toastTimer) window.clearTimeout(toastTimer)
})

// ---------------- 提交 ----------------

function round(v: number): number {
  return Math.round(v * 100) / 100
}

function commitDraft(d: DraftState, snap: boolean): void {
  const start = { time: d.start.time, price: round(d.start.price) }
  const end = { time: d.end.time, price: round(d.end.price) }
  store.addAnnotation({ kind: d.kind, start, end, snap })
}

function finishText(d: DraftState, snap: boolean): void {
  const text = window.prompt('文字标注内容：')
  if (!text || !text.trim()) return
  store.addAnnotation({
    kind: 'text',
    start: { time: d.start.time, price: round(d.start.price) },
    text: text.trim(),
    snap,
  })
}

// ---------------- 标注的命中与移动 ----------------

function beginMove(ev: PointerEvent, id: string): void {
  if (store.tool !== 'select' || !store.canOperate) return
  const a = (store.instance?.annotations ?? []).find((item) => item.id === id)
  if (!a) return
  ev.stopPropagation()
  ev.preventDefault()
  store.selectedAnnotation = a.id
  const p = localPoint(ev)
  if (!p) return
  const idxStart = scale.indexOfTime(a.start.time)
  const idxEnd = scale.indexOfTime(a.end.time)
  if (idxStart === undefined) return
  drag = {
    ...emptyDrag(),
    mode: 'move',
    startX: p.x,
    startY: p.y,
    lastX: p.x,
    lastY: p.y,
    snap: ev.ctrlKey,
    moveId: a.id,
    moveKind: a.kind,
    moveText: a.text ?? '',
    moveColor: a.color ?? '#f59e0b',
    baseStart: a.start,
    baseEnd: a.end,
    baseIndex: idxStart,
    baseIndexEnd: idxEnd ?? idxStart,
    basePrice: a.start.price,
    basePriceEnd: a.end.price,
    downPrice: scale.priceAtY(p.y),
  }
  moving.value = {
    id: a.id,
    kind: a.kind,
    text: a.text ?? '',
    color: a.color ?? '#f59e0b',
    start: { time: a.start.time, price: a.start.price },
    end: { time: a.end.time, price: a.end.price },
    snap: ev.ctrlKey,
  }
  beginPointerCapture()
}

function onAnnotationClick(id: string): void {
  if (store.tool !== 'select') return
  if (lastDragMoved) {
    lastDragMoved = false
    return
  }
  store.selectedAnnotation = store.selectedAnnotation === id ? null : id
}

// ---------------- 键盘 ----------------

function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

function onKeyDown(ev: KeyboardEvent): void {
  ctrlDown.value = ev.ctrlKey
  if (isTypingTarget(ev.target)) return
  if (ev.key === 'Escape') {
    draft.value = null
    moving.value = null
    drag = emptyDrag()
    endPointerCapture()
    store.selectedAnnotation = null
    menu.value = null
    requestDraw()
    return
  }
  if ((ev.key === 'Delete' || ev.key === 'Backspace') && store.selectedAnnotation) {
    ev.preventDefault()
    store.deleteAnnotation(store.selectedAnnotation)
    store.selectedAnnotation = null
  }
}

function onKeyUp(ev: KeyboardEvent): void {
  ctrlDown.value = ev.ctrlKey
}

// ---------------- SVG 图层 ----------------

interface ScreenShape {
  id: string
  kind: string
  x1: number
  y1: number
  x2: number
  y2: number
  text?: string
  textWidth: number
  color: string
  snapped: boolean
  selected: boolean
}

let measureCtx: CanvasRenderingContext2D | null = null
function measureText(text: string): number {
  if (!measureCtx) {
    const c = document.createElement('canvas')
    measureCtx = c.getContext('2d')
  }
  if (!measureCtx) return text.length * 12
  measureCtx.font = '13px "Segoe UI", system-ui, "Microsoft YaHei", sans-serif'
  return measureCtx.measureText(text).width
}

const shapes = computed<ScreenShape[]>(() => {
  void viewTick.value
  const inst = store.instance
  if (!inst) return []
  const out: ScreenShape[] = []
  for (const a of inst.annotations) {
    const s = moving.value?.id === a.id ? moving.value : null
    const start = toScreen(s ? s.start : a.start)
    const end = toScreen(s ? s.end : a.end)
    if (!start) continue
    const kind = s ? s.kind : a.kind
    out.push({
      id: a.id,
      kind,
      x1: start.x,
      y1: start.y,
      x2: end?.x ?? start.x,
      y2: end?.y ?? start.y,
      text: s ? s.text : a.text,
      textWidth: measureText(s ? s.text : (a.text ?? '')),
      color: s ? s.color : (a.color ?? '#f59e0b'),
      snapped: s ? s.snap : a.snapped,
      selected: store.selectedAnnotation === a.id,
    })
  }
  return out
})

/** 绘制/移动过程中的幽灵图形（只在本机，不发给后端） */
const ghost = computed<ScreenShape | null>(() => {
  void viewTick.value
  const d = draft.value
  if (d) {
    const start = toScreen(d.start)
    const end = toScreen(d.end)
    if (!start || !end) return null
    return {
      id: 'ghost',
      kind: d.kind,
      x1: start.x,
      y1: start.y,
      x2: end.x,
      y2: end.y,
      text: '',
      textWidth: 0,
      color: store.color,
      snapped: d.snap,
      selected: false,
    }
  }
  const m = moving.value
  if (m) {
    const start = toScreen(m.start)
    const end = toScreen(m.end)
    if (!start) return null
    return {
      id: 'ghost-move',
      kind: m.kind,
      x1: start.x,
      y1: start.y,
      x2: end?.x ?? start.x,
      y2: end?.y ?? start.y,
      text: m.text,
      textWidth: measureText(m.text),
      color: m.color,
      snapped: m.snap,
      selected: false,
    }
  }
  return null
})

const snapLabel = computed(() => {
  const p = draft.value ?? null
  const m = moving.value ?? null
  if (p?.snap || m?.snap) return '已吸附到 OHLC'
  return ctrlDown.value ? '按住 Ctrl：吸附 OHLC' : ''
})

function rectBox(s: ScreenShape): { x: number; y: number; w: number; h: number } {
  const x = Math.min(s.x1, s.x2)
  const y = Math.min(s.y1, s.y2)
  return { x, y, w: Math.abs(s.x2 - s.x1), h: Math.abs(s.y2 - s.y1) }
}

const priceHint = computed(() => {
  const h = hover.value
  if (!h) return ''
  const bar = scale.bars[h.index]
  if (!bar) return ''
  // 和十字光标同一个口径：价格先吸附到最小跳动，再显示
  const p = snapToTick(scale.priceAtY(h.y), store.tick)
  return `${formatAxisTime(bar.time)}   ${formatPrice(p, store.priceDecimals)}`
})

defineExpose({ requestDraw })
</script>

<template>
  <div
    ref="host"
    class="relative h-full w-full select-none overflow-hidden"
    style="cursor: crosshair"
    @wheel="onWheel"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerleave="onPointerLeave"
    @dblclick="onDoubleClick"
    @contextmenu="onContextMenu"
  >
    <canvas ref="canvas" class="absolute inset-0 block h-full w-full" />

    <svg
      class="absolute inset-0 h-full w-full"
      :viewBox="`0 0 ${size.width} ${size.height}`"
      preserveAspectRatio="none"
      style="pointer-events: none"
    >
      <g
        v-for="s in shapes"
        :key="s.id"
        :style="{ pointerEvents: store.tool === 'select' ? 'auto' : 'none' }"
        :class="store.tool === 'select' ? 'cursor-move' : ''"
        @pointerdown="beginMove($event, s.id)"
        @click.stop="onAnnotationClick(s.id)"
      >
        <!-- 选中框 -->
        <rect
          v-if="s.selected"
          :x="Math.min(s.x1, s.x2) - 4"
          :y="Math.min(s.y1, s.y2) - 4"
          :width="Math.abs(s.x2 - s.x1) + 8"
          :height="Math.abs(s.y2 - s.y1) + 8"
          fill="none"
          :stroke="theme.selection"
          stroke-width="1"
          stroke-dasharray="3 3"
          opacity="0.7"
        />
        <!-- 矩形 -->
        <template v-if="s.kind === 'rect'">
          <rect
            :x="rectBox(s).x"
            :y="rectBox(s).y"
            :width="rectBox(s).w"
            :height="rectBox(s).h"
            :stroke="s.color"
            :fill="s.color"
            fill-opacity="0.1"
            :stroke-width="s.selected ? 2 : 1.5"
          />
        </template>
        <!-- 线段 -->
        <template v-else-if="s.kind === 'line'">
          <line
            :x1="s.x1"
            :y1="s.y1"
            :x2="s.x2"
            :y2="s.y2"
            :stroke="s.color"
            :stroke-width="s.selected ? 2.5 : 1.6"
          />
          <circle :cx="s.x1" :cy="s.y1" r="2.6" :fill="s.color" />
          <circle :cx="s.x2" :cy="s.y2" r="2.6" :fill="s.color" />
        </template>
        <!-- 文字 -->
        <template v-else>
          <rect
            :x="s.x1 - 3"
            :y="s.y1 - 9"
            :width="s.textWidth + 8"
            height="18"
            :fill="labelBg"
            :stroke="s.selected ? s.color : 'transparent'"
            stroke-width="1"
            rx="2"
          />
          <text
            :x="s.x1 + 1"
            :y="s.y1 + 1"
            :fill="s.color"
            font-size="13"
            font-family='"Segoe UI", system-ui, "Microsoft YaHei", sans-serif'
            dominant-baseline="middle"
          >
            {{ s.text }}
          </text>
        </template>
      </g>

      <!-- 幽灵图形：只在本机显示，松手后由后端广播的正式图形替换 -->
      <g v-if="ghost" style="pointer-events: none">
        <template v-if="ghost.kind === 'rect'">
          <rect
            :x="Math.min(ghost.x1, ghost.x2)"
            :y="Math.min(ghost.y1, ghost.y2)"
            :width="Math.abs(ghost.x2 - ghost.x1)"
            :height="Math.abs(ghost.y2 - ghost.y1)"
            :stroke="ghost.color"
            fill="none"
            stroke-width="1.4"
            stroke-dasharray="4 3"
          />
        </template>
        <template v-else-if="ghost.kind === 'line'">
          <line
            :x1="ghost.x1"
            :y1="ghost.y1"
            :x2="ghost.x2"
            :y2="ghost.y2"
            :stroke="ghost.color"
            stroke-width="1.4"
            stroke-dasharray="4 3"
          />
        </template>
        <template v-else>
          <text
            :x="ghost.x1 + 1"
            :y="ghost.y1 + 1"
            :fill="ghost.color"
            font-size="13"
            font-family='"Segoe UI", system-ui, "Microsoft YaHei", sans-serif'
            dominant-baseline="middle"
            opacity="0.8"
          >
            {{ ghost.text }}
          </text>
        </template>
        <circle
          v-if="ghost.snapped"
          :cx="ghost.x2"
          :cy="ghost.y2"
          r="4"
          fill="none"
          :stroke="theme.selection"
          stroke-width="1"
        />
      </g>
    </svg>

    <!--
      提示文字放在时间轴**上方**（bottom = 时间轴高度 + 4px）。
      以前贴在 bottom-1，正好压在时间标签上，一眼全是重影。
    -->
    <div
      class="pointer-events-none absolute right-2 flex items-baseline gap-3 text-[12px] text-faint"
      :style="{ bottom: `${TIME_AXIS_HEIGHT + 4}px` }"
    >
      <span v-if="priceHint" class="tnum text-dim">{{ priceHint }}</span>
      <span>滚轮缩放 · Shift+滚轮缩放价格 · 拖动平移 · 双击重置价格 · 右键挂单/复制价格</span>
    </div>
    <div
      v-if="snapLabel"
      class="pointer-events-none absolute top-2 left-2 rounded bg-elev px-2 py-1 text-[12px] text-amber-600 ring-1 ring-line dark:text-amber-300"
    >
      {{ snapLabel }}
    </div>
    <div
      v-if="store.instance && !store.instance.bars.length"
      class="absolute inset-0 flex items-center justify-center text-[15px] text-dim"
    >
      这个实例还没有 K 线
    </div>

    <!--
      右键小菜单：价格在打开那一刻就算好（吸附到最小跳动），三项动作都基于这个价格。
      菜单自身要 stop 掉 pointerdown —— 否则画布的 pointerdown 会先把菜单关掉，
      click 就永远到不了按钮上。
    -->
    <div
      v-if="menu"
      class="absolute z-40 overflow-hidden rounded border border-line bg-panel py-1 text-[12px] text-ink shadow-2xl"
      :style="menuStyle"
      @pointerdown.stop
      @contextmenu.stop.prevent
    >
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-elev disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate"
        @click="menuOrder('buy', 'limit')"
      >
        <span class="h-0 w-0 border-x-[4px] border-b-[6px] border-x-transparent border-b-emerald-500" />
        买入 {{ store.orderQuantity }} 手 {{ menuPriceText }} 限价
      </button>
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-elev disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate"
        @click="menuOrder('sell', 'stop')"
      >
        <span class="h-0 w-0 border-x-[4px] border-t-[6px] border-x-transparent border-t-red-500" />
        卖出 {{ store.orderQuantity }} 手 {{ menuPriceText }} 止损
      </button>
      <div class="my-1 h-px bg-line" />
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-elev"
        @click="menuCopy"
      >
        <span class="h-2 w-2 shrink-0 rounded-[2px] border border-current opacity-60" />
        复制价格 {{ menuPriceText }}
      </button>
    </div>

    <div
      v-if="toast"
      class="pointer-events-none absolute top-2 left-1/2 -translate-x-1/2 rounded bg-elev px-3 py-1 text-[12px] text-ink shadow-lg ring-1 ring-line"
    >
      {{ toast }}
    </div>
  </div>
</template>
