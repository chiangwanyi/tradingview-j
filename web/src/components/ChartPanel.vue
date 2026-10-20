<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { buildFigure, resolveStyle, type Figure, type FigureView } from '@/components/chart/annotations'
import { orderKindFor, orderKindLabel } from '@/components/chart/orders'
import { ORDER_RESERVE_RIGHT, drawChart, themeOf, type HoverState } from '@/components/chart/render'
import { ChartScale, TIME_AXIS_HEIGHT, clamp, formatAxisTime, formatPrice, snapToTick } from '@/components/chart/scale'
import { closedTrades, type ClosedTrade } from '@/components/chart/trades'
import TradeTipCard from '@/components/TradeTipCard.vue'
import { TOOL_LABEL, isDrawTool, useReplayStore, type DrawTool } from '@/stores/replay'
import type { Bar, Order, OrderKind, Point, Side } from '@/types/protocol'

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
 *     终点可以越过最后一根 K 线往右画：横坐标存成「锚点 K 线 + 向右几根」（见 scale.ts 的 barRefAt），
 *     所以它既在右侧空白区里，又仍然严格落在时间-价格坐标系上。
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
 * 菜单的前三项都作用在这个价格上（买入 / 卖出 / 复制价格），所以价格必须在打开那一刻就定死，
 * 不能等点击时再从鼠标位置算一遍 —— 手一抖价格就变了。（第四项「移除 N 个绘图」不看价格。）
 *
 * 单型（限价/止损）也不让用户选：由**这个价格相对现价的方向**算出来
 * （见 `chart/orders.ts` 的 `orderKindFor`）。现有价才有方向，所以菜单里先把现价写出来，
 * 用户一眼就能看出「我这一下是在现价上方还是下方、买到的是哪种单」。
 */
const menu = ref<{ x: number; y: number; price: number } | null>(null)
/**
 * 「移除 N 个绘图」按下一次之后的上膛状态：**第二次单击才真的删**。
 *
 * 这个动作删的是当前实例上**所有人**画的图，而且没有撤销 —— 手滑一下代价太大，
 * 所以用「同一行变红再点一次」代替弹窗：不打断操作，也不用再解释一遍。
 * 菜单一关就复位（见 onContextMenu / 各条关闭路径）。
 */
const clearArmed = ref(false)
/** 复制价格/挂单成功后的一句轻提示（1.4 秒自己消失） */
const toast = ref('')
let toastTimer = 0

interface ChartPoint {
  /** 锚点 K 线的时间（一定是已揭示数据里真实存在的一根） */
  time: string
  /** 从锚点往右延伸的根数：0 = 就在这根 K 线上，>0 = 越过最后一根、画在右侧空白区 */
  offset: number
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

/**
 * 拖动中的挂单：这一份只在本机显示，松手才发给后端。
 *
 * 为什么不在拖动过程中就发：鼠标每移动 1px 就是一条 WS 消息，后端会广播回来，
 * 多人演示时其他人看到的挂单线会一路抖。拖动期间本机按这个价格画，松手一次定稿。
 */
const orderDrag = ref<{ id: string; price: number } | null>(null)
/** 指针停在挂单线上：换光标、把那条线画粗（挂单是 Canvas 画的，得自己拾取） */
const hoverOrderId = ref<string | null>(null)

/**
 * 平仓虚线的悬停信息卡。
 *
 * 三种状态，缺一不可：
 *   - `tip`：跟随鼠标，鼠标一离开这条线就销毁（指针离开画布同理）；
 *   - `pinned`：鼠标 3 秒没动就把它钉在画布上 —— 这时可以拖动、可以点 × 关掉；
 *   - 都没有：什么都不显示。
 *
 * `x/y` 在 `tip` 里是**鼠标位置**（渲染时算偏移），在 `pinned` 里是**卡片左上角**（拖动直接改它）。
 */
interface TradeTip {
  index: number
  trade: ClosedTrade
  x: number
  y: number
}

const tip = ref<TradeTip | null>(null)
const pinned = ref<TradeTip | null>(null)
/** 钉住的等待时间：鼠标不动满这么久才固定 */
const PIN_DELAY = 3000
let pinTimer = 0
/** 卡片尺寸（用来做边缘翻转与拖动边界，和 TradeTipCard 的实际大小是同一档） */
const TIP_W = 196
const TIP_H = 132

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
    orders: effectiveOrders(),
    trades,
    closed: closed.value,
    position: inst?.position ?? { quantity: 0, side: 'flat', averagePrice: 0 },
    unrealizedPnl: inst?.unrealizedPnl ?? 0,
    showEma: store.showEma,
    tick: store.tick,
    activeOrderId: activeOrderId.value,
    activeClosed: activeClosed.value,
  })
}

/** 画布上的挂单：拖动中的那一张换成**本机价格**，其余原样 */
function effectiveOrders(): Order[] {
  const inst = store.instance
  if (!inst) return []
  const d = orderDrag.value
  return d ? inst.orders.map((o) => (o.id === d.id ? { ...o, price: d.price } : o)) : inst.orders
}

/** 已配好对的「开仓 → 平仓」，画线、命中、信息卡用的是同一份 */
const closed = computed(() => closedTrades(store.instance?.trades ?? []))
/** 加粗哪一条挂单：拖动优先于悬停（拖动时鼠标其实一直在线上） */
const activeOrderId = computed(() => orderDrag.value?.id ?? hoverOrderId.value)
/** 加粗哪一条平仓线：跟随卡优先，其次是钉住的卡 */
const activeClosed = computed(() => tip.value?.index ?? pinned.value?.index ?? null)

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
  window.removeEventListener('pointermove', onPinMove)
  window.removeEventListener('pointerup', onPinUp)
  if (rafId) cancelAnimationFrame(rafId)
  if (toastTimer) window.clearTimeout(toastTimer)
  if (pinTimer) window.clearTimeout(pinTimer)
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
    orderDrag.value = null
    hoverOrderId.value = null
    clearTip()
    pinned.value = null
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

/**
 * 指针位置 -> 数据坐标（锚点 K 线时间 + 向右偏移 + 价格）。Ctrl 按下时价格吸附到锚点 K 线的 OHLC。
 *
 * 横向**不再夹到最后一根 K 线**：x 只夹在价格区左右两条边上（右边是价格轴，不是行情区），
 * 夹完交给 `barRefAt` 换算 —— 越过末尾的部分会自动变成「最后一根 + N 根」。
 * 所以「往右拖到未来」和「拖到某根历史上的 K 线」走的是同一套换算，不会偏移。
 */
function pointAt(x: number, y: number, snap: boolean): ChartPoint | null {
  if (scale.bars.length === 0) return null
  const plot = scale.layout.plot
  const ref = scale.barRefAt(scale.indexAtX(clamp(x, plot.left, plot.right)))
  if (!ref) return null
  const idx = scale.indexOfTime(ref.time)
  const bar = idx === undefined ? undefined : scale.bars[idx]
  if (!bar) return null
  // 吸附用的是**锚点**那根 K 线：偏移出去的地方一根 K 线都没有，没有 OHLC 可吸。
  // 后端 resolvePoint 也是拿锚点那根吸的，所以预览和定稿结果一致。
  const price = snap ? nearestOHLC(bar, scale.priceAtY(y)) : scale.priceAtY(y)
  return { time: ref.time, offset: ref.offset, price }
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
    // 端点 = 锚点时间 + 向右偏移；virtualIndexOf 允许越过最后一根，所以右侧空白区里
    // 的端点用得和 K 线完全相同的 x 公式（缩放/平移一起动，不会有半根线的错位）。
    xOf: (point) => {
      const idx = scale.virtualIndexOf(point)
      return idx === undefined ? undefined : scale.xOf(idx)
    },
    yOfPrice: (price) => scale.yOf(price),
    priceText: (price) => formatPrice(price, store.priceDecimals),
  }
}

// ---------------- 指针交互 ----------------

interface DragState {
  mode: 'none' | 'pan' | 'move' | 'order'
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
  /** 端点锚定的 K 线下标 */
  baseIndex: number
  baseIndexEnd: number
  /** 端点从锚点往右延伸的根数（>0 = 在最后一根 K 线的右侧） */
  baseOffset: number
  baseOffsetEnd: number
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
    baseOffset: 0,
    baseOffsetEnd: 0,
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
  closeMenu()
  // 按下 = 要开始拖东西了：跟随卡先收掉（否则它会留在原地，看着像没跟上手）
  clearTip()
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

  // 指针 / 修改模式：先看有没有按在**挂单线**上（画在 Canvas 上，没有 DOM 命中测试）。
  // 挂单在网格、K 线之上，视觉上就是可以抓的东西，所以优先于平移。
  const hitOrder = orderAt(p.x, p.y)
  if (hitOrder) {
    if (!store.canOperate) {
      showToast('现在只能看：先「接管」操作权再改挂单')
      return
    }
    const target = store.instance?.orders.find((o) => o.id === hitOrder)
    if (!target) return
    drag = {
      ...emptyDrag(),
      mode: 'order',
      startX: p.x,
      startY: p.y,
      lastX: p.x,
      lastY: p.y,
      moveId: target.id,
      basePrice: target.price,
      downPrice: scale.priceAtY(p.y),
    }
    orderDrag.value = { id: target.id, price: target.price }
    beginPointerCapture()
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
  // 只改价格，横向（锚点 + 偏移）保持鼠标指的位置 —— 否则 Shift 会把线拽回起点那根 K 线上
  return { time: end.time, offset: end.offset, price: start.price }
}

/** 两个端点完全重合 = 没画出东西（横坐标和时间都相同、价格也相同） */
function degenerate(start: ChartPoint, end: ChartPoint): boolean {
  // 横坐标要比「锚点 + 偏移」两项：同一根 K 线上的 offset=0 和 offset=3 是两个位置
  return start.time === end.time && start.offset === end.offset && Math.abs(start.price - end.price) < 1e-9
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

  if (drag.mode === 'order') {
    // 拖动改价：价格吸附到 tick（和十字光标、右键菜单同一把尺子），
    // 所以拖到哪儿都是这个标的上真实存在的报价
    if (Math.abs(p.y - drag.startY) > 1) drag.moved = true
    orderDrag.value = { id: drag.moveId, price: snapToTick(scale.priceAtY(p.y), store.tick) }
    requestDraw()
    return
  }

  if (drag.mode === 'move' && moving.value) {
    drag.snap = ev.ctrlKey
    const deltaBars = Math.round((p.x - drag.startX) / scale.barWidth)
    const deltaPrice = scale.priceAtY(p.y) - drag.downPrice

    // 整体移动：两个端点一起走；拖端点：只动那一个（另一个原样留着）
    const moveStart = drag.anchor === -1 || drag.anchor === 0
    const moveEnd = drag.anchor === -1 || drag.anchor === 1

    // 端点先化成**连续下标**（锚点下标 + 向右偏移）再平移，于是「越过最后一根」的端点
    // 和历史里的端点走的是同一条算术；barRefAt 负责把结果重新表达成
    // 「真实 K 线 + 根数」，并把下标夹在 [第一根, 最后一根 + 上限] 里。
    const refStart = scale.barRefAt(drag.baseIndex + drag.baseOffset + (moveStart ? deltaBars : 0))
    const refEnd = scale.barRefAt(drag.baseIndexEnd + drag.baseOffsetEnd + (moveEnd ? deltaBars : 0))
    if (!refStart || !refEnd) return
    const idxStart = scale.indexOfTime(refStart.time)
    const idxEnd = scale.indexOfTime(refEnd.time)
    if (idxStart === undefined || idxEnd === undefined) return
    const barStart = scale.bars[idxStart]
    const barEnd = scale.bars[idxEnd]
    if (!barStart || !barEnd) return
    const rawStart = drag.basePrice + (moveStart ? deltaPrice : 0)
    const rawEnd = drag.basePriceEnd + (moveEnd ? deltaPrice : 0)
    moving.value = {
      id: drag.moveId,
      start: {
        time: refStart.time,
        offset: refStart.offset,
        price: drag.snap ? nearestOHLC(barStart, rawStart) : rawStart,
      },
      end: {
        time: refEnd.time,
        offset: refEnd.offset,
        price: drag.snap ? nearestOHLC(barEnd, rawEnd) : rawEnd,
      },
    }
    drag.moved = true
    requestDraw()
  }
}

function onWindowPointerUp(): void {
  if (drag.mode === 'order') {
    const moved = orderDrag.value
    // 只在真的换了价格时才发：点一下（没拖）等于什么都没发生，
    // 否则操作记录里会多出一条「改价 6484.00 → 6484.00」的噪音
    if (moved && drag.moved && Math.abs(moved.price - drag.basePrice) > 1e-9) {
      lastDragMoved = true
      store.moveOrder(moved.id, moved.price)
    }
    orderDrag.value = null
  } else if (drag.mode === 'move' && moving.value && drag.moved) {
    lastDragMoved = true
    const m = moving.value
    // 整个端点对象发过去（含 offset）：只发 time+price 的话，服务端会把「越过最后一根」
    // 的端点吸回锚点那根 K 线上，松手即跳。只动一个端点时另一个原样发回。
    store.moveAnnotation(
      m.id,
      { ...m.start, price: round(m.start.price) },
      { ...m.end, price: round(m.end.price) },
      drag.snap,
    )
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

  // 挂单线：换光标 + 画粗（拖拽改价的入口）
  hoverOrderId.value = orderAt(p.x, p.y)
  // 平仓虚线：悬停出信息卡（绘图工具下不抢：那条线会挡住「找终点」）
  if (drawMode.value) clearTip()
  else updateTradeTip(p.x, p.y)

  requestDraw()
}

function onPointerLeave(): void {
  hover.value = null
  hoverOrderId.value = null
  ctrlDown.value = false
  shiftDown.value = false
  // 鼠标移出画布 = 离开那条线：跟随卡销毁（钉住的卡是用户自己固定下来的，留着）
  clearTip()
  requestDraw()
}

function onWheel(ev: WheelEvent): void {
  closeMenu()
  if (!store.instance) return
  ev.preventDefault()
  const p = localPoint(ev)
  if (!p) return
  const factor = ev.deltaY > 0 ? 1 / 1.12 : 1.12
  // 右侧价格轴 = 价格区的旋钮：指针在那儿滚轮就缩价格，在行情区滚轮就缩时间。
  // （以前是 Shift+滚轮缩价格 —— 但「在价格轴上一滚就能缩」是看图时的本能，
  //   而且不用先找准 Shift。Shift 现在在画布上不再有缩放含义。）
  //
  // 注意价格轴的系数要取倒数：`zoomTime` 的 factor 是乘在 **barWidth** 上（大 = 放大），
  // 而 `zoomPrice` 的 factor 是乘在 **离锚点的距离** 上（大 = 缩小）。同一个滚轮方向
  // 两边必须给出相反的系数，否则「往上滚」在时间轴是放大、在价格轴却成了缩小。
  if (p.x >= scale.layout.plot.right) scale.zoomPrice(1 / factor, p.y)
  else scale.zoomTime(factor, p.x)
  requestDraw()
}

function onDoubleClick(): void {
  // 绘图时双击是「两次单击」，不能顺手把价格轴重置了
  if (drawMode.value) return
  scale.resetPrice()
  requestDraw()
}

// ---------------- 画布上的拾取：挂单线 / 平仓虚线 ----------------

/** 挂单线的命中容差：线只有 1px，光标得有点容错，但不能大到「隔着两条线也能抓」 */
const ORDER_HIT_PX = 6
/**
 * 平仓虚线的命中容差。
 *
 * 6px 是折中量出来的：3px 以下时，1.2px 的虚线得瞄准着放才碰得到；10px 以上时，
 * 鼠标只是从线旁边经过就会弹出卡片挡住 K 线，而且两条线挨得近时分不清指的是哪条。
 */
const TRADE_HIT_PX = 6

/**
 * 指针下面的挂单 id（只认还挂着的、画在可见区里的）。
 *
 * 挂单是 Canvas 画的，没有 DOM 元素可点，命中测试只能自己算：把价格换成 y 坐标取最近的一条。
 * 多条挨在一起时取**最近**的，不是数组里最后一条 —— 否则两张价格几乎相同的单里，
 * 永远只能抓到后面挂的那张。
 */
function orderAt(x: number, y: number): string | null {
  const plot = scale.layout.plot
  if (x < plot.left || x > plot.right || y < plot.top || y > plot.bottom) return null
  let bestId: string | null = null
  let bestDist = ORDER_HIT_PX + 1
  for (const o of effectiveOrders()) {
    if (o.status !== 'open') continue
    const oy = scale.yOf(o.price)
    if (oy < plot.top || oy > plot.bottom) continue
    const d = Math.abs(oy - y)
    if (d < bestDist) {
      bestId = o.id
      bestDist = d
    }
  }
  return bestId
}

/** 指针下面的平仓虚线（取最近的一条） */
function closedAt(x: number, y: number): TradeTip | null {
  const plot = scale.layout.plot
  if (x < plot.left || x > plot.right || y < plot.top || y > plot.bottom) return null
  const list = closed.value
  let best: { index: number; trade: ClosedTrade; d: number } | null = null
  for (let i = 0; i < list.length; i += 1) {
    const c = list[i]
    const i1 = scale.indexOfTime(c.openTime)
    const i2 = scale.indexOfTime(c.closeTime)
    if (i1 === undefined || i2 === undefined) continue
    const d = distanceToSegment(
      x,
      y,
      scale.xOf(i1),
      scale.yOf(c.openPrice),
      scale.xOf(i2),
      scale.yOf(c.closePrice),
    )
    if (d <= TRADE_HIT_PX && (!best || d < best.d)) best = { index: i, trade: c, d }
  }
  return best ? { index: best.index, trade: best.trade, x, y } : null
}

/** 点到线段的距离：判断「悬浮在平仓线上」的全部几何都在这里 */
function distanceToSegment(
  px: number,
  py: number,
  x1: number,
  y1: number,
  x2: number,
  y2: number,
): number {
  const dx = x2 - x1
  const dy = y2 - y1
  const len2 = dx * dx + dy * dy
  if (len2 <= 1e-9) return Math.hypot(px - x1, py - y1)
  // 投影参数夹到 [0,1]：落在线段外时，距离算到最近的那个端点（不会有无限延伸的命中区）
  const t = clamp(((px - x1) * dx + (py - y1) * dy) / len2, 0, 1)
  return Math.hypot(px - (x1 + t * dx), py - (y1 + t * dy))
}

// ---------------- 平仓线的信息卡 ----------------

/**
 * 鼠标每动一次就更新跟随卡并**重新计时**：
 * 「3 秒没动」是从最后一次移动算起的，不是从第一次悬浮算起的。
 */
function updateTradeTip(x: number, y: number): void {
  // 已经钉住时不再出跟随卡：否则屏幕上同时两张卡说同一件事
  if (pinned.value) return
  const hit = closedAt(x, y)
  if (!hit) {
    clearTip()
    return
  }
  tip.value = hit
  armPinTimer()
}

/** 销毁跟随卡（鼠标移出那条线、移出画布、开始拖东西时都走它） */
function clearTip(): void {
  if (pinTimer) {
    window.clearTimeout(pinTimer)
    pinTimer = 0
  }
  tip.value = null
}

function armPinTimer(): void {
  if (pinTimer) window.clearTimeout(pinTimer)
  pinTimer = window.setTimeout(() => {
    pinTimer = 0
    const t = tip.value
    if (!t) return
    // 固定在它**此刻所在的位置**（而不是鼠标位置）：否则卡片会从右下角突然跳到左上角
    const box = tipBox.value
    pinned.value = { ...t, x: box.left, y: box.top }
    tip.value = null
    requestDraw()
  }, PIN_DELAY)
}

/** 跟随卡的左上角：默认在鼠标右下方 16px，贴近边缘时翻到另一侧，并夹在画布内 */
const tipBox = computed(() => {
  const t = tip.value
  if (!t) return { left: 0, top: 0 }
  let left = t.x + 16
  let top = t.y + 16
  if (left + TIP_W > size.value.width - 8) left = t.x - TIP_W - 16
  if (top + TIP_H > size.value.height - 8) top = t.y - TIP_H - 16
  return {
    left: clamp(left, 8, Math.max(8, size.value.width - TIP_W - 8)),
    top: clamp(top, 8, Math.max(8, size.value.height - TIP_H - 8)),
  }
})

const tipStyle = computed(() => ({
  left: `${tipBox.value.left}px`,
  top: `${tipBox.value.top}px`,
  width: `${TIP_W}px`,
}))

const pinnedStyle = computed(() => {
  const p = pinned.value
  if (!p) return {}
  return { left: `${p.x}px`, top: `${p.y}px`, width: `${TIP_W}px` }
})

/** 钉住之后拖着走：按下卡片（× 除外）就在画布内平移它 */
let pinDrag: { dx: number; dy: number } | null = null

function beginPinDrag(ev: PointerEvent): void {
  const p = pinned.value
  if (!p) return
  // 卡片长在画布里：不 stop 的话按下会冒到画布的 pointerdown，顺手把整个视图也拖了
  ev.stopPropagation()
  ev.preventDefault()
  const pt = localPoint(ev)
  if (!pt) return
  pinDrag = { dx: pt.x - p.x, dy: pt.y - p.y }
  window.addEventListener('pointermove', onPinMove)
  window.addEventListener('pointerup', onPinUp)
}

function onPinMove(ev: PointerEvent): void {
  const p = pinned.value
  const pt = localPoint(ev)
  if (!pinDrag || !p || !pt) return
  pinned.value = {
    ...p,
    x: clamp(pt.x - pinDrag.dx, 4, Math.max(4, size.value.width - TIP_W - 4)),
    y: clamp(pt.y - pinDrag.dy, 4, Math.max(4, size.value.height - TIP_H - 4)),
  }
}

function onPinUp(): void {
  pinDrag = null
  window.removeEventListener('pointermove', onPinMove)
  window.removeEventListener('pointerup', onPinUp)
}

function closePinned(): void {
  pinned.value = null
  requestDraw()
}

// ---------------- 挂单的「×」按钮（HTML 叠在画布上） ----------------

interface AxisOrder {
  id: string
  side: Side
  left: number
  top: number
  title: string
}

/**
 * 每张挂单右侧的撤单按钮位置。
 *
 * 为什么用 HTML 而不是画在 Canvas 上：按钮需要 hover 反馈、title 提示、禁用态，
 * 这些 Canvas 全要手写；HTML 一个 button 就都有了。位置仍然由 `ChartScale` 算，
 * 和画布上那条线严格对齐（同一个 yOf）。
 */
const axisOrders = computed<AxisOrder[]>(() => {
  void viewTick.value
  const plot = scale.layout.plot
  const out: AxisOrder[] = []
  for (const o of effectiveOrders()) {
    if (o.status !== 'open') continue
    const y = scale.yOf(o.price)
    if (y < plot.top || y > plot.bottom) continue
    out.push({
      id: o.id,
      side: o.side,
      left: plot.right - ORDER_RESERVE_RIGHT + 4,
      top: y - 8,
      title: `撤单：${o.side === 'buy' ? '买' : '卖'} ${o.quantity} 手 @ ${formatPrice(o.price, store.priceDecimals)}`,
    })
  }
  return out
})

function cancelFromCanvas(id: string): void {
  if (!store.canOperate) {
    showToast('现在只能看：先「接管」操作权再撤单')
    return
  }
  store.cancelOrder(id)
}

// ---------------- 提交 ----------------

function round(v: number): number {
  return Math.round(v * 100) / 100
}

/**
 * 第 2 次单击：把时间 + 价格 + **当前模板的样式快照**发往后端。
 *
 * 用 `...p.start` 而不是逐字段重建：offset 也是端点的一部分，漏掉它后端就会把端点
 * 落回锚点那根 K 线 —— 预览明明伸到了右边，一松手却缩回去（这里踩过一次）。
 */
function commitPending(p: PendingShape): void {
  const template = store.templateFor(p.kind)
  store.addAnnotation({
    kind: p.kind,
    start: { ...p.start, price: round(p.start.price) },
    end: { ...p.end, price: round(p.end.price) },
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
    // 端点可能停在最后一根右侧（offset > 0）：拖动的起点必须把它算进去，
    // 否则一按下整条线就会往左跳 offset 根。
    baseOffset: a.start.offset ?? 0,
    baseOffsetEnd: a.end.offset ?? 0,
    basePrice: a.start.price,
    basePriceEnd: a.end.price,
    downPrice: scale.priceAtY(p.y),
  }
  moving.value = {
    id: a.id,
    start: { time: a.start.time, offset: a.start.offset ?? 0, price: a.start.price },
    end: { time: a.end.time, offset: a.end.offset ?? 0, price: a.end.price },
  }
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
  const w = 264
  const h = 206
  return {
    left: `${Math.max(4, Math.min(m.x, size.value.width - w - 4))}px`,
    top: `${Math.max(4, Math.min(m.y, size.value.height - h - 4))}px`,
    width: `${w}px`,
  }
})

/** 关菜单 = 顺手把「移除绘图」的上膛状态也退掉（关掉再打开不该还是一点就删） */
function closeMenu(): void {
  menu.value = null
  clearArmed.value = false
}

function onContextMenu(ev: MouseEvent): void {
  ev.preventDefault()
  if (!store.instance) return
  const p = localPoint(ev)
  if (!p) return
  // 每次打开都从「没上膛」开始：上一次按了一半就走开，不该让下一次一点就删
  clearArmed.value = false
  menu.value = { x: p.x, y: p.y, price: snapToTick(scale.priceAtY(p.y), store.tick) }
}

/** 当前画布上有几个绘图 —— 菜单里那个 n，也是「一次会删掉几个」 */
const annotationCount = computed(() => store.instance?.annotations.length ?? 0)

/**
 * 移除全部绘图。
 *
 * 第一下只是上膛（文案变红、写明不可撤销），第二下才发消息。发出去之后**本地不做
 * 乐观清空**：后端广播 `annotation_cleared` 回来才算数，被拒时画布保持原样。
 *
 * 手里那些「指向某个图形」的临时状态必须一起丢掉：画到一半的预览、正在拖的图形、
 * 选中的那个 id —— 它们指向的图形马上就要不存在了，留着会让下一次按键打到空气上。
 */
function menuClearAnnotations(): void {
  if (annotationCount.value === 0) return
  if (!store.canOperate) {
    showToast('现在只能看：先「接管」操作权再移除绘图')
    closeMenu()
    return
  }
  if (!clearArmed.value) {
    clearArmed.value = true
    return
  }
  const n = annotationCount.value
  store.clearAnnotations()
  pending.value = null
  moving.value = null
  store.selectedAnnotation = null
  clearArmed.value = false
  closeMenu()
  requestDraw()
  showToast(`已移除 ${n} 个绘图`)
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
    closeMenu()
    return
  }
  const kind = orderKindFor(side, m.price, lastPrice.value)
  store.placeOrder(side, kind, m.price, store.orderQuantity)
  closeMenu()
  showToast(
    `已挂 ${side === 'buy' ? '买入' : '卖出'}${orderKindLabel(kind)} ${formatPrice(m.price, store.priceDecimals)}`,
  )
}

async function menuCopy(): Promise<void> {
  const m = menu.value
  if (!m) return
  const text = formatPrice(m.price, store.priceDecimals)
  const ok = await copyText(text)
  closeMenu()
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
    closeMenu()
    // 钉住的信息卡也一起收掉：Esc 在这个画布上的含义是「退掉最上面那一层」
    pinned.value = null
    clearTip()
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
    // 「可以画过最后一根」值得写在提示里：以前终点会被吸回最后一根，用户会以为工具坏了
    const beyond = ' · 可画过最后一根'
    return pending.value
      ? `${name}：移动鼠标找终点，再单击一下定稿${extra}${beyond} · Esc 取消`
      : `${name}：单击定起点${extra}${beyond} · 滚轮缩放 · 中键拖动平移`
  }
  if (modifyMode.value) {
    return '单击选中 · 拖端点改端点 · 拖线身整体移动 · Del 删除 · 空白处拖动平移 · 拖挂单线改价'
  }
  return '滚轮缩放 · 右轴滚轮缩价格 · 拖动平移 · 拖挂单线改价 · 双击重置价格 · 右键挂单'
})

/**
 * 画布光标：把「这里能做什么」写在指针上。
 *
 * 挂单线可以上下拖（ns-resize）、平仓虚线有信息卡（help）—— 这两种都是画在 Canvas 上的，
 * 没有 DOM 能自动给出 cursor，只能按命中结果自己切。
 */
const cursorStyle = computed(() => {
  if (drawMode.value) return 'crosshair'
  if (orderDrag.value) return 'grabbing'
  if (hoverOrderId.value) return 'ns-resize'
  if (tip.value) return 'help'
  return 'default'
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
    :style="{ cursor: cursorStyle }"
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
      每张挂单一个「×」：位置由 ChartScale 算（和画布上那条线同一个 yOf），
      横向让在挂单标签的右边 —— 标签绘制时已经用 ORDER_RESERVE_RIGHT 让出了这块地方。
      pointerdown.stop 是必须的：不拦住就会冒到画布的 pointerdown，顺手把视图也拖一下。
      没有操作权时保留按钮但禁用（灰掉）：直接藏起来会让人以为「这些单撤不了」。
    -->
    <button
      v-for="o in axisOrders"
      :key="`cancel-${o.id}`"
      type="button"
      class="absolute z-20 flex h-4 w-4 items-center justify-center rounded-[3px] text-[12px] leading-none ring-1 transition disabled:cursor-not-allowed disabled:opacity-30"
      :class="
        o.side === 'buy'
          ? 'bg-panel text-emerald-700 ring-emerald-500/60 hover:bg-emerald-600 hover:text-white dark:text-emerald-300'
          : 'bg-panel text-red-700 ring-red-500/60 hover:bg-red-600 hover:text-white dark:text-red-300'
      "
      :style="{ left: `${o.left}px`, top: `${o.top}px` }"
      :title="o.title"
      :disabled="!store.canOperate"
      @pointerdown.stop
      @click.stop="cancelFromCanvas(o.id)"
      @contextmenu.stop.prevent
    >
      ×
    </button>

    <!--
      平仓线的跟随卡：pointer-events-none 是硬要求 —— 卡片一旦能接指针，
      鼠标「停在卡片上」就不再落在虚线上，「移出线就销毁」永远触发不了。
    -->
    <div v-if="tip" class="pointer-events-none absolute z-30" :style="tipStyle">
      <TradeTipCard :trade="tip.trade" />
    </div>

    <!-- 3 秒没动就固定下来的那张：可以拖（整个卡是把手），右上角 × 关闭 -->
    <div
      v-if="pinned"
      class="absolute z-30 cursor-move select-none"
      :style="pinnedStyle"
      @pointerdown="beginPinDrag"
      @contextmenu.stop.prevent
    >
      <TradeTipCard :trade="pinned.trade" pinned @close="closePinned" />
    </div>

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
      右键小菜单：价格在打开那一刻就算好（吸附到最小跳动），前三项动作都基于这个价格；
      第四项「移除 N 个绘图」与价格无关，按的是整个画布的绘图总数。
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

      <!--
        「移除 N 个绘图」：N 是当前画布上的绘图总数（= 这一下会删掉几个）。
        第一次单击只是上膛（变红 + 写明不可撤销），第二次才真删 —— 一次删光所有人
        画的图且不能撤销，值得多问一遍。灰掉的理由只有两个：没有绘图、没有操作权。
      -->
      <div class="my-1 h-px bg-line" />
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left disabled:cursor-not-allowed disabled:opacity-40"
        :class="
          clearArmed
            ? 'bg-red-50 text-red-700 hover:bg-red-100 dark:bg-red-950/50 dark:text-red-300 dark:hover:bg-red-950'
            : 'hover:bg-elev'
        "
        :disabled="annotationCount === 0 || !store.canOperate"
        :title="
          annotationCount === 0
            ? '当前画布上还没有绘图'
            : !store.canOperate
              ? '现在只能看：先「接管」操作权再移除绘图'
              : '删掉当前画布上全部绘图（同实例所有人都会看不到，不能撤销）'
        "
        @click="menuClearAnnotations"
      >
        <span class="h-2 w-2 shrink-0 rounded-[2px] border border-current opacity-60" />
        {{
          annotationCount === 0
            ? '移除 0 个绘图'
            : clearArmed
              ? `确认移除 ${annotationCount} 个绘图（不可撤销）`
              : `移除 ${annotationCount} 个绘图`
        }}
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
