<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { buildFigure, resolveStyle, type Figure, type FigureView } from '@/components/chart/annotations'
import { orderKindFor, orderKindLabel } from '@/components/chart/orders'
import { drawChart, themeOf, type HoverState } from '@/components/chart/render'
import { ChartScale, TIME_AXIS_HEIGHT, formatAxisTime, formatPrice, snapToTick } from '@/components/chart/scale'
import { closedTrades } from '@/components/chart/trades'
import { TOOL_LABEL, isDrawTool, useReplayStore, type DrawTool } from '@/stores/replay'
import type { Bar, OrderKind, Point, Side } from '@/types/protocol'

/**
 * 画布 = Canvas（K 线/指标/挂单价线/十字光标）+ SVG（标注图形）。
 *
 * 为什么要分两层：
 *   - K 线一次几百根、每帧重画，Canvas 最快；
 *   - 标注要能被指针命中、能拖、能选中，SVG 元素天然有 hit-test，不用自己写拾取算法。
 *
 * 四种模式（`store.tool`），指针行为完全不同：
 *
 *   - 指针 `select`：只缩放/平移，不碰标注；
 *   - 修改 `modify`：点选标注、拖端点改端点、拖线身整体移动、Del 删除；
 *   - 五种绘图工具：**两次单击** —— 第 1 次定起点进入预览，移动鼠标找终点，第 2 次定稿。
 *     （不再是按住拖动：拖动手势在触控板和大图上都容易画歪，两次单击可以慢慢找位置。）
 *
 * 「前端预览、后端定稿」没变：绘制过程只更新本地的 `pending`，第 2 次单击才把
 * **时间 + 价格 + 样式**发往后端；后端校验、编号后广播回来，界面上的正式图形永远
 * 来自广播。所以后端拒绝时幽灵图形一消失，界面上什么都不会留下。
 */

const store = useReplayStore()

/** 画布配色跟着主题走：两套配色都在 render.ts 里，这里只选一套 */
const theme = computed(() => themeOf(store.theme))

const host = ref<HTMLDivElement | null>(null)
const canvas = ref<HTMLCanvasElement | null>(null)
const scale = new ChartScale()

const size = ref({ width: 0, height: 0 })
/** 视图变化（缩放/平移/尺寸）时自增，用来让依赖坐标的 computed 重新计算 */
const viewTick = ref(0)
const hover = ref<HoverState | null>(null)
const ctrlDown = ref(false)
const shiftDown = ref(false)

/**
 * 右键小菜单：位置（画布内的本地像素）+ **已经吸附到最小跳动**的价格。
 *
 * 菜单只提供「按这个价格挂单 / 复制这个价格」，所以价格必须在打开那一刻就定死，
 * 不能等点击时再从鼠标位置算一遍 —— 手一抖价格就变了。
 *
 * 单型（限价/止损）也不让用户选：由**这个价格相对现价的方向**算出来
 * （见 `chart/orders.ts` 的 `orderKindFor`）。现有价才有方向，所以菜单里先把现价写出来，
 * 用户一眼就能看出「我这一下是在现价上方还是下方、买到的是哪种单」。
 */
const menu = ref<{ x: number; y: number; price: number } | null>(null)
/** 复制价格/挂单成功后的一句轻提示（1.4 秒自己消失） */
const toast = ref('')
let toastTimer = 0

interface ChartPoint {
  time: string
  price: number
}

/** 两次单击绘制的中间状态：第 1 次单击已定起点，`end` 跟着鼠标走。 */
interface PendingShape {
  kind: DrawTool
  start: ChartPoint
  end: ChartPoint
  snap: boolean
  /** 最近一次移动/单击时按没按 Shift（线类工具 = 强制水平） */
  shift: boolean
}

/** 拖动中的标注：这一份只在本机显示，松手才发给后端 */
interface MovingShape {
  id: string
  start: ChartPoint
  end: ChartPoint
}

const pending = ref<PendingShape | null>(null)
const moving = ref<MovingShape | null>(null)
/** 上一次拖动是否真的移动过：拖完松手会补一个 click，别让它把选中状态点没了 */
let lastDragMoved = false

const drawMode = computed(() => isDrawTool(store.tool))
const modifyMode = computed(() => store.tool === 'modify')

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
    unrealizedPnl: inst?.unrealizedPnl ?? 0,
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
  if (toastTimer) window.clearTimeout(toastTimer)
})

// 实例换了：重置视图（否则会拿着上一个标的的缩放去画新标的）
watch(
  () => store.instance?.id,
  () => {
    scale.setBars(store.bars)
    scale.resetView()
    hover.value = null
    pending.value = null
    moving.value = null
    requestDraw()
  },
)

// 换工具 = 放弃手里那次没画完的图形，并清掉选中（两种模式管的东西不一样）
watch(
  () => store.tool,
  () => {
    pending.value = null
    moving.value = null
    store.selectedAnnotation = null
    requestDraw()
  },
)

// 新增一根 K 线：**只追加渲染，不动视图**。
//
// 旧版这里调了 scrollToTail() + fitPrice(true)，于是每点一次「下一根」整屏 K 线就向左滑一格，
// 正在看的那段历史、画在上面的标注全都跟着跑掉 —— 这里把自动移动彻底去掉：
// scroll / barWidth / priceMin / priceMax 一概不碰。已有 K 线的下标没变、scroll 没变，
// xOf() 算出来的像素位置就逐点相同；新 K 线只是按自己的下标画在右侧
// （落在可见区外就先看不见，等用户自己拖过去）。
watch(
  () => store.bars.length,
  () => {
    scale.setBars(store.bars)
    // 追加时 maxScroll 只会变大，clampScroll 不会动到现有位置；
    // 但数据被整体替换（重连后重新对齐）时 bars 可能变短，越界的 scroll 得拉回来。
    scale.clampScroll()
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
// 浮盈只在步进时变（按新 K 线收盘价重算），成本线标签要跟着更新
watch(
  () => store.instance?.unrealizedPnl,
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

/** 画图形用的坐标换算（chart/annotations.ts 只认这一份接口，不认识缩放对象） */
function figureView(): FigureView {
  return {
    plot: scale.layout.plot,
    xOfTime: (time) => {
      const idx = scale.indexOfTime(time)
      return idx === undefined ? undefined : scale.xOf(idx)
    },
    yOfPrice: (price) => scale.yOf(price),
    priceText: (price) => formatPrice(price, store.priceDecimals),
  }
}

// ---------------- 指针交互 ----------------

interface DragState {
  mode: 'none' | 'pan' | 'move'
  startX: number
  startY: number
  lastX: number
  lastY: number
  moved: boolean
  snap: boolean
  /** 正在拖的标注 */
  moveId: string
  /** 拖的是哪个位置：-1 = 整体，0 = 起点，1 = 终点 */
  anchor: -1 | 0 | 1
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
    anchor: -1,
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

  // 中键：任何模式下都平移
  if (ev.button === 1) {
    ev.preventDefault()
    drag = { ...emptyDrag(), mode: 'pan', startX: p.x, startY: p.y, lastX: p.x, lastY: p.y }
    beginPointerCapture()
    return
  }
  if (ev.button !== 0) return

  // 绘图工具：两次单击。第 1 次定起点，第 2 次定终点。
  if (drawMode.value) {
    clickToDraw(p, ev)
    return
  }

  // 指针 / 修改模式在空白处按下 = 平移（修改模式下按在图形上的事件被 SVG 拦掉了）
  drag = { ...emptyDrag(), mode: 'pan', startX: p.x, startY: p.y, lastX: p.x, lastY: p.y, snap: ev.ctrlKey }
  beginPointerCapture()
}

/** 绘图工具的单击：没有起点就定起点，有起点就定终点并提交。 */
function clickToDraw(p: { x: number; y: number }, ev: PointerEvent): void {
  const tool = store.tool
  if (!isDrawTool(tool)) return
  if (!store.canOperate) {
    // 后端会拒（没有操作权），但在画布上静默失败最难排查 —— 直接说清楚
    showToast('现在只能看：先「接管」操作权再画')
    return
  }
  const hit = pointAt(p.x, p.y, ev.ctrlKey)
  if (!hit) return
  shiftDown.value = ev.shiftKey

  const cur = pending.value
  if (!cur || cur.kind !== tool) {
    pending.value = { kind: tool, start: hit, end: hit, snap: ev.ctrlKey, shift: ev.shiftKey }
    requestDraw()
    return
  }

  const end = constrainEnd(tool, cur.start, hit, ev.shiftKey)
  if (degenerate(cur.start, end)) {
    // 两次点在同一根 K 线的同一个价位：当成「重新开始」，不产生一个看不见的零长度图形
    pending.value = { kind: tool, start: hit, end: hit, snap: ev.ctrlKey, shift: ev.shiftKey }
    requestDraw()
    return
  }
  commitPending({ ...cur, end, snap: ev.ctrlKey, shift: ev.shiftKey })
}

/**
 * Shift 约束：趋势线/射线/箭头线按住 Shift 就是水平线（两个端点同一个价格）。
 * 矩形和斐波那契不受影响 —— 它们的语义本来就是「一个区间」。
 */
function constrainEnd(kind: DrawTool, start: ChartPoint, end: ChartPoint, shift: boolean): ChartPoint {
  const lineLike = kind === 'trend' || kind === 'ray' || kind === 'arrow'
  if (!shift || !lineLike) return end
  return { time: end.time, price: start.price }
}

/** 两个端点完全重合 = 没画出东西（时间相同、价格也相同） */
function degenerate(start: ChartPoint, end: ChartPoint): boolean {
  return start.time === end.time && Math.abs(start.price - end.price) < 1e-9
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
  shiftDown.value = ev.shiftKey

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

  if (drag.mode === 'move' && moving.value) {
    drag.snap = ev.ctrlKey
    const bars = scale.bars
    const maxIdx = Math.max(0, bars.length - 1)
    const deltaBars = Math.round((p.x - drag.startX) / scale.barWidth)
    const deltaPrice = scale.priceAtY(p.y) - drag.downPrice

    // 整体移动：两个端点一起走；拖端点：只动那一个（另一个原样留着）
    const moveStart = drag.anchor === -1 || drag.anchor === 0
    const moveEnd = drag.anchor === -1 || drag.anchor === 1

    const idxStart = Math.min(maxIdx, Math.max(0, drag.baseIndex + (moveStart ? deltaBars : 0)))
    const idxEnd = Math.min(maxIdx, Math.max(0, drag.baseIndexEnd + (moveEnd ? deltaBars : 0)))
    const barStart = bars[idxStart]
    const barEnd = bars[idxEnd]
    if (!barStart || !barEnd) return
    const rawStart = drag.basePrice + (moveStart ? deltaPrice : 0)
    const rawEnd = drag.basePriceEnd + (moveEnd ? deltaPrice : 0)
    moving.value = {
      id: drag.moveId,
      start: { time: barStart.time, price: drag.snap ? nearestOHLC(barStart, rawStart) : rawStart },
      end: { time: barEnd.time, price: drag.snap ? nearestOHLC(barEnd, rawEnd) : rawEnd },
    }
    drag.moved = true
    requestDraw()
  }
}

function onWindowPointerUp(): void {
  if (drag.mode === 'move' && moving.value && drag.moved) {
    lastDragMoved = true
    const m = moving.value
    store.moveAnnotation(m.id, { time: m.start.time, price: round(m.start.price) }, { time: m.end.time, price: round(m.end.price) }, drag.snap)
  } else if (drag.mode === 'pan' && !drag.moved) {
    // 在空白处点一下 = 取消选中（和桌面上点空白处一个意思）
    store.selectedAnnotation = null
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
  shiftDown.value = ev.shiftKey
  if (drag.mode !== 'none') return

  // 两次单击绘制的中间态：终点跟着鼠标走（不需要按住任何键）
  if (pending.value) {
    const aim = pointAt(p.x, p.y, ev.ctrlKey)
    if (aim) {
      pending.value = {
        ...pending.value,
        end: constrainEnd(pending.value.kind, pending.value.start, aim, ev.shiftKey),
        snap: ev.ctrlKey,
        shift: ev.shiftKey,
      }
    }
  }

  const idx = scale.nearestIndex(p.x)
  hover.value = { index: idx, x: p.x, y: p.y, inside: true }
  requestDraw()
}

function onPointerLeave(): void {
  hover.value = null
  ctrlDown.value = false
  shiftDown.value = false
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
  // 绘图时双击是「两次单击」，不能顺手把价格轴重置了
  if (drawMode.value) return
  scale.resetPrice()
  requestDraw()
}

// ---------------- 提交 ----------------

function round(v: number): number {
  return Math.round(v * 100) / 100
}

/** 第 2 次单击：把时间 + 价格 + **当前模板的样式快照**发往后端。 */
function commitPending(p: PendingShape): void {
  const template = store.templateFor(p.kind)
  store.addAnnotation({
    kind: p.kind,
    start: { time: p.start.time, price: round(p.start.price) },
    end: { time: p.end.time, price: round(p.end.price) },
    style: resolveStyle({ kind: p.kind, style: template?.style }),
    snap: p.snap,
  })
  pending.value = null
  requestDraw()
}

// ---------------- 标注的命中、选中与拖动 ----------------

/** 在「修改」模式下按下图形本体：整体移动。 */
function beginBodyDrag(ev: PointerEvent, id: string): void {
  beginAnnotationDrag(ev, id, -1)
}

/** 按下端点手柄：只移动这一个端点。 */
function beginAnchorDrag(ev: PointerEvent, id: string, anchor: 0 | 1): void {
  beginAnnotationDrag(ev, id, anchor)
}

function beginAnnotationDrag(ev: PointerEvent, id: string, anchor: -1 | 0 | 1): void {
  if (!modifyMode.value || !store.canOperate) return
  const a = (store.instance?.annotations ?? []).find((item) => item.id === id)
  if (!a) return
  ev.stopPropagation()
  ev.preventDefault()
  // 这里**不置选中**：选中交给 click（没拖动才会走到）。否则 pointerdown 先选中、
  // click 又按「再点一次取消」把它翻掉，拖完一手图形就变回未选中。
  const p = localPoint(ev)
  if (!p) return
  const idxStart = scale.indexOfTime(a.start.time)
  if (idxStart === undefined) return
  const idxEnd = scale.indexOfTime(a.end.time)
  drag = {
    ...emptyDrag(),
    mode: 'move',
    startX: p.x,
    startY: p.y,
    lastX: p.x,
    lastY: p.y,
    snap: ev.ctrlKey,
    moveId: a.id,
    anchor,
    baseStart: a.start,
    baseEnd: a.end,
    baseIndex: idxStart,
    baseIndexEnd: idxEnd ?? idxStart,
    basePrice: a.start.price,
    basePriceEnd: a.end.price,
    downPrice: scale.priceAtY(p.y),
  }
  moving.value = { id: a.id, start: { ...a.start }, end: { ...a.end } }
  beginPointerCapture()
}

/** 点一下（没拖动）就选中/取消选中。 */
function onAnnotationClick(id: string): void {
  if (!modifyMode.value) return
  if (lastDragMoved) {
    lastDragMoved = false
    return
  }
  store.selectedAnnotation = store.selectedAnnotation === id ? null : id
}

// ---------------- 右键小菜单 ----------------

/**
 * 现价 = 游标所在（最后一根已揭示）K 线的收盘价。
 *
 * 为什么是它、而不是鼠标下面那根 K 线的收盘价：挂单类型是按「挂单价相对**下单那一刻的市价**」
 * 定的，而复盘里「下单那一刻」永远是游标那根（后端 `placeOrder` 也拿 `currentBar()` 做基准）。
 * 鼠标可以停在一百根之前的历史价位上，那个价格不是现在的市价。
 */
const lastPrice = computed(() => store.currentBar?.close ?? 0)

const lastPriceText = computed(() =>
  lastPrice.value > 0 ? formatPrice(lastPrice.value, store.priceDecimals) : '—',
)

/** 菜单里那句价格：三位小数（BTCUSDT 0.1 一跳）也不会被截成两位 */
const menuPriceText = computed(() => (menu.value ? formatPrice(menu.value.price, store.priceDecimals) : ''))

/** 菜单价格在现价的哪一侧 —— 只用来给菜单写一句话，判定本身在 orderKindFor 里 */
const menuSideText = computed(() => {
  const m = menu.value
  if (!m || !(lastPrice.value > 0)) return ''
  if (m.price > lastPrice.value) return '高于现价'
  if (m.price < lastPrice.value) return '低于现价'
  return '等于现价'
})

/**
 * 两个按钮各自的单型。菜单价格在打开那一刻就固定了，所以这两个值也是固定的：
 * 现价下方 → 买限价 / 卖止损；现价上方 → 买止损 / 卖限价。
 */
const buyKind = computed<OrderKind>(() =>
  menu.value ? orderKindFor('buy', menu.value.price, lastPrice.value) : 'limit',
)
const sellKind = computed<OrderKind>(() =>
  menu.value ? orderKindFor('sell', menu.value.price, lastPrice.value) : 'limit',
)

/** 靠边右键时把菜单挪回画布内，别一半露在窗口外面 */
const menuStyle = computed(() => {
  const m = menu.value
  if (!m) return {}
  const w = 236
  const h = 142
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

/**
 * 下单。单型**在这里再算一次**（不是从模板传进来）：按钮文案和执行必须是同一个判定，
 * 否则将来改文案时很容易出现「写着限价、发出去是止损」。
 */
function menuOrder(side: Side): void {
  const m = menu.value
  if (!m) return
  if (!store.canOperate) {
    showToast('现在只能看：先「接管」操作权再下单')
    menu.value = null
    return
  }
  const kind = orderKindFor(side, m.price, lastPrice.value)
  store.placeOrder(side, kind, m.price, store.orderQuantity)
  menu.value = null
  showToast(
    `已挂 ${side === 'buy' ? '买入' : '卖出'}${orderKindLabel(kind)} ${formatPrice(m.price, store.priceDecimals)}`,
  )
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

// ---------------- 键盘 ----------------

function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

function onKeyDown(ev: KeyboardEvent): void {
  ctrlDown.value = ev.ctrlKey
  shiftDown.value = ev.shiftKey
  if (isTypingTarget(ev.target)) return

  if (ev.key === 'Escape') {
    if (pending.value) {
      // 第一次：只是放弃手里这次没画完的图形
      pending.value = null
    } else if (drawMode.value) {
      // 第二次：退出绘图工具回到指针模式
      store.tool = 'select'
    }
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
  shiftDown.value = ev.shiftKey
}

// ---------------- SVG 图层 ----------------

/**
 * 已画好的图形。拖动中的那一个用 `moving` 里的端点代替 —— 所以拖的时候图形是
 * 跟着手指动的（不是另画一个幽灵），松手后才由后端广播定稿。
 */
const figures = computed<Figure[]>(() => {
  void viewTick.value
  const inst = store.instance
  if (!inst) return []
  const view = figureView()
  const out: Figure[] = []
  for (const a of inst.annotations) {
    const over = moving.value?.id === a.id ? moving.value : null
    const f = buildFigure(
      {
        id: a.id,
        kind: a.kind,
        start: over ? over.start : a.start,
        end: over ? over.end : a.end,
        style: a.style,
        color: a.color,
      },
      view,
    )
    if (f) out.push(f)
  }
  return out
})

/** 两次单击之间、跟着鼠标走的预览图形（用当前模板的样式，所以选模板时就所见即所得） */
const ghost = computed<Figure | null>(() => {
  void viewTick.value
  const p = pending.value
  if (!p) return null
  return buildFigure(
    { id: 'ghost', kind: p.kind, start: p.start, end: p.end, style: store.templateFor(p.kind)?.style },
    figureView(),
  )
})

/** 选中时显示端点手柄（只有修改模式才有「选中的图形」这个概念） */
const selectedId = computed(() => (modifyMode.value ? store.selectedAnnotation : null))

const hint = computed(() => {
  if (drawMode.value) {
    const name = TOOL_LABEL[store.tool]
    const extra = store.tool === 'trend' || store.tool === 'ray' || store.tool === 'arrow' ? ' · Shift 水平' : ''
    return pending.value
      ? `${name}：移动鼠标找终点，再单击一下定稿${extra} · Esc 取消`
      : `${name}：单击定起点${extra} · 滚轮缩放 · 中键拖动平移`
  }
  if (modifyMode.value) {
    return '单击选中 · 拖端点改端点 · 拖线身整体移动 · Del 删除 · 空白处拖动平移'
  }
  return '滚轮缩放 · Shift+滚轮缩放价格 · 拖动平移 · 双击重置价格 · 右键挂单/复制价格'
})

/** 按住 Ctrl / Shift 时给一句提示，省得用户不知道有这么个东西 */
const modifierLabel = computed(() => {
  if (ctrlDown.value) return 'Ctrl：吸附 OHLC'
  if (shiftDown.value && drawMode.value && (store.tool === 'trend' || store.tool === 'ray' || store.tool === 'arrow')) {
    return 'Shift：水平线'
  }
  if (pending.value?.snap) return '已吸附 OHLC'
  return ''
})

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
    :style="{ cursor: drawMode ? 'crosshair' : 'default' }"
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
      <!--
        已画好的图形。只有「修改」模式下才对指针可见 —— 其他模式下鼠标事件要落到
        下面的画布上（平移、绘制起点），不能让一条线挡住。
      -->
      <g
        v-for="f in figures"
        :key="f.id"
        :style="{ pointerEvents: modifyMode ? 'auto' : 'none' }"
        :class="modifyMode ? 'cursor-move' : ''"
        @pointerdown="beginBodyDrag($event, f.id)"
        @click.stop="onAnnotationClick(f.id)"
      >
        <!-- 矩形：填充 + 边框 -->
        <rect
          v-if="f.box"
          :x="f.box.x"
          :y="f.box.y"
          :width="f.box.w"
          :height="f.box.h"
          :stroke="f.box.color"
          :stroke-width="f.box.width"
          :stroke-dasharray="f.box.dash"
          :fill="f.box.fill"
          :fill-opacity="f.box.fillOpacity"
        />
        <!-- 线段：一条可见的 + 一条加粗透明的（命中区，否则 1px 的线根本点不中） -->
        <template v-for="(s, i) in f.segs" :key="i">
          <line
            :x1="s.x1"
            :y1="s.y1"
            :x2="s.x2"
            :y2="s.y2"
            :stroke="s.color"
            :stroke-width="s.width"
            :stroke-dasharray="s.dash"
            :opacity="s.opacity"
          />
          <line
            v-if="modifyMode"
            :x1="s.x1"
            :y1="s.y1"
            :x2="s.x2"
            :y2="s.y2"
            stroke="rgba(0,0,0,0)"
            stroke-width="12"
          />
        </template>
        <!-- 箭头 -->
        <polygon v-if="f.head" :points="f.head.points" :fill="f.head.color" />
        <!-- 斐波那契的水平标签：比例 + 价格，贴在每条线的右端 -->
        <text
          v-for="(c, i) in f.chips"
          :key="`t${i}`"
          :x="c.x"
          :y="c.y"
          :fill="c.color"
          :text-anchor="c.anchor"
          font-size="11"
          font-family='"Segoe UI", system-ui, "Microsoft YaHei", sans-serif'
          class="tnum"
        >
          {{ c.text }}
        </text>
        <!-- 选中：虚线包围框 + 两个端点手柄 -->
        <template v-if="selectedId === f.id">
          <rect
            :x="Math.min(f.anchors[0].x, f.anchors[1].x) - 6"
            :y="Math.min(f.anchors[0].y, f.anchors[1].y) - 6"
            :width="Math.abs(f.anchors[1].x - f.anchors[0].x) + 12"
            :height="Math.abs(f.anchors[1].y - f.anchors[0].y) + 12"
            fill="none"
            :stroke="theme.selection"
            stroke-width="1"
            stroke-dasharray="3 3"
            opacity="0.7"
          />
          <circle
            v-for="(a, i) in f.anchors"
            :key="`h${i}`"
            :cx="a.x"
            :cy="a.y"
            r="4.5"
            :fill="theme.bg"
            :stroke="theme.selection"
            stroke-width="1.5"
            style="cursor: grab"
            @pointerdown.stop="beginAnchorDrag($event, f.id, i === 0 ? 0 : 1)"
          />
        </template>
      </g>

      <!-- 预览图形：只在本机显示，第 2 次单击后由后端广播的正式图形替换 -->
      <g v-if="ghost" style="pointer-events: none" opacity="0.92">
        <rect
          v-if="ghost.box"
          :x="ghost.box.x"
          :y="ghost.box.y"
          :width="ghost.box.w"
          :height="ghost.box.h"
          :stroke="ghost.box.color"
          :stroke-width="ghost.box.width"
          :stroke-dasharray="ghost.box.dash"
          :fill="ghost.box.fill"
          :fill-opacity="ghost.box.fillOpacity"
        />
        <line
          v-for="(s, i) in ghost.segs"
          :key="i"
          :x1="s.x1"
          :y1="s.y1"
          :x2="s.x2"
          :y2="s.y2"
          :stroke="s.color"
          :stroke-width="s.width"
          :stroke-dasharray="s.dash"
        />
        <polygon v-if="ghost.head" :points="ghost.head.points" :fill="ghost.head.color" />
        <text
          v-for="(c, i) in ghost.chips"
          :key="`t${i}`"
          :x="c.x"
          :y="c.y"
          :fill="c.color"
          :text-anchor="c.anchor"
          font-size="11"
          font-family='"Segoe UI", system-ui, "Microsoft YaHei", sans-serif'
          class="tnum"
        >
          {{ c.text }}
        </text>
        <!-- 起点标记：一眼看出「第 1 次单击落在哪儿了」 -->
        <circle
          v-if="pending"
          :cx="ghost.anchors[0].x"
          :cy="ghost.anchors[0].y"
          r="3.5"
          fill="none"
          :stroke="theme.selection"
          stroke-width="1.5"
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
      <span>{{ hint }}</span>
    </div>
    <div
      v-if="modifierLabel"
      class="pointer-events-none absolute top-2 left-2 rounded bg-elev px-2 py-1 text-[12px] text-amber-600 ring-1 ring-line dark:text-amber-300"
    >
      {{ modifierLabel }}
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
      单型跟着「价格在现价的哪一侧」走：下方 = 买限价 / 卖止损，上方 = 买止损 / 卖限价。
    -->
    <div
      v-if="menu"
      class="absolute z-40 overflow-hidden rounded border border-line bg-panel py-1 text-[12px] text-ink shadow-2xl"
      :style="menuStyle"
      @pointerdown.stop
      @contextmenu.stop.prevent
    >
      <div class="flex items-center justify-between gap-2 border-b border-line px-3 pb-1 text-[11px] text-faint">
        <span>现价 <span class="tnum text-dim">{{ lastPriceText }}</span></span>
        <span>{{ menuSideText }}</span>
      </div>
      <button
        type="button"
        class="mt-1 flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-elev disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate"
        :title="
          buyKind === 'limit'
            ? '价格低于现价：等回落到这个价位才买（限价买）'
            : '价格高于现价：涨到这个价位才追多（止损买）'
        "
        @click="menuOrder('buy')"
      >
        <span class="h-0 w-0 border-x-[4px] border-b-[6px] border-x-transparent border-b-emerald-500" />
        买入 {{ store.orderQuantity }} 手 {{ menuPriceText }} {{ orderKindLabel(buyKind) }}
      </button>
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-elev disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate"
        :title="
          sellKind === 'limit'
            ? '价格高于现价：等涨到这个价位才卖（限价卖）'
            : '价格低于现价：跌破这个价位才卖（止损卖）'
        "
        @click="menuOrder('sell')"
      >
        <span class="h-0 w-0 border-x-[4px] border-t-[6px] border-x-transparent border-t-red-500" />
        卖出 {{ store.orderQuantity }} 手 {{ menuPriceText }} {{ orderKindLabel(sellKind) }}
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
