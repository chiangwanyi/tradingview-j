import type { AnnotationStyle, DashStyle, FibLevel } from '@/types/protocol'

/**
 * 标注图形 -> 屏幕图元。
 *
 * 为什么单独一层：五种图形在 SVG 里长得完全不一样（一条线 / 一条射线 / 一个矩形 /
 * 一条带箭头的线 / 四条水平线），但**渲染是同一套**（线段、矩形、箭头、文字标签）。
 * 这里把「图形语义」翻译成「几个线段 + 一个矩形 + 一个箭头 + 几个标签」，
 * ChartPanel 的模板里就只有一层 v-for，不用给每种图形写一段 SVG。
 *
 * 换算用的 x / y 由调用方注入（ChartPanel 的 scale），这一层不认识缩放和平移，
 * 所以它每次都是「按当前视图重新算一遍」—— 射线延伸到屏幕边缘、斐波那契四条线的
 * 高度，都会跟着缩放自动变。这也是为什么不把像素存进后端。
 */

export interface FigurePoint {
  time: string
  price: number
}

export interface FigureInput {
  id: string
  kind: string
  start: FigurePoint
  end: FigurePoint
  style?: AnnotationStyle
  /** 0.5.2 及以前的单一颜色，旧存档里还带着 */
  color?: string
}

export interface FigureView {
  /** 价格区（不含时间轴和价格轴）：射线只延伸到这块区域的边上 */
  plot: { left: number; top: number; right: number; bottom: number }
  /** K 线时间 -> 屏幕 x；不在已揭示的数据里返回 undefined */
  xOfTime(time: string): number | undefined
  yOfPrice(price: number): number
  /** 价格 -> 显示文本（斐波那契的水平标签用） */
  priceText(price: number): string
}

export interface Seg {
  x1: number
  y1: number
  x2: number
  y2: number
  color: string
  width: number
  dash: string
  opacity: number
}

export interface Box {
  x: number
  y: number
  w: number
  h: number
  color: string
  width: number
  dash: string
  fill: string
  fillOpacity: number
}

/** 文字标签。anchor='end' 表示 x 是文字右端（斐波那契的标签贴在线的右端） */
export interface Chip {
  x: number
  y: number
  text: string
  color: string
  anchor: 'start' | 'end'
}

export interface Head {
  points: string
  color: string
}

/** 一个图形在屏幕上要画的所有东西。 */
export interface Figure {
  id: string
  kind: string
  segs: Seg[]
  box: Box | null
  head: Head | null
  chips: Chip[]
  /** 两个端点（选中时画手柄，拖动时命中） */
  anchors: { x: number; y: number }[]
}

// ---------------- 样式 ----------------

/** 斐波那契的四个比例。后端 annotation.go 里的 fibRatios 是同一份，顺序也一样。 */
export const FIB_RATIOS = [-1, 0, 1, 2] as const

/** 四个水平的出厂颜色（和后端 levelColorByRatio 一致） */
export const FIB_COLORS: Record<number, string> = {
  [-1]: '#38bdf8',
  0: '#f59e0b',
  1: '#ec4899',
  2: '#22c55e',
}

/** SVG 的 stroke-dasharray：实线就是空串。 */
export function dashArray(dash: DashStyle | undefined): string {
  return dash === 'dashed' ? '6 4' : ''
}

/** 一种图形的出厂样式。后端模板缺失时前端用它兜底，保证「一定能画出来」。 */
export function defaultStyle(kind: string): AnnotationStyle {
  switch (kind) {
    case 'rect':
      return { color: '#f59e0b', width: 1.4, dash: 'solid', fill: '#f59e0b', fillOpacity: 0.12 }
    case 'fib':
      return { color: '#f59e0b', width: 1.2, dash: 'solid', levels: defaultLevels() }
    default:
      return { color: '#f59e0b', width: 1.6, dash: 'solid' }
  }
}

export function defaultLevels(): FibLevel[] {
  return FIB_RATIOS.map((r) => ({ ratio: r, color: FIB_COLORS[r] }))
}

/**
 * 把「可能不全」的样式补成「一定完整」的样式。
 *
 * 三处会遇到不完整：旧存档（只有 color）、后端模板被手工改过、以及兜底路径。
 * 规则和后端 sanitizeStyle 一致（线宽 1~6、只有 solid/dashed、斐波那契固定四条）。
 */
export function resolveStyle(input: { kind: string; style?: AnnotationStyle; color?: string }): AnnotationStyle {
  const base = defaultStyle(input.kind)
  const s: Partial<AnnotationStyle> = input.style ?? {}
  const width = s.width && Number.isFinite(s.width) ? Math.min(6, Math.max(1, s.width)) : base.width
  const style: AnnotationStyle = {
    color: s.color || input.color || base.color,
    width,
    dash: s.dash === 'dashed' ? 'dashed' : 'solid',
  }
  if (input.kind === 'rect') {
    style.fill = s.fill || style.color
    style.fillOpacity = s.fillOpacity ?? base.fillOpacity ?? 0.12
  }
  if (input.kind === 'fib') {
    style.levels = resolveLevels(s.levels)
  }
  return style
}

/** 只认 -1/0/1/2 四个比例，缺的补出厂色，顺序固定 —— 界面上永远不会出现第五条线。 */
function resolveLevels(inLevels: FibLevel[] | undefined): FibLevel[] {
  const byRatio = new Map<number, string>()
  for (const lv of inLevels ?? []) {
    if (lv && typeof lv.color === 'string' && lv.color) byRatio.set(lv.ratio, lv.color)
  }
  return FIB_RATIOS.map((r) => ({ ratio: r, color: byRatio.get(r) ?? FIB_COLORS[r] }))
}

// ---------------- 几何 ----------------

/** 箭头头部的长度（像素）。线段会相应缩短一点，免得线尖从三角形里穿出来。 */
const HEAD_LEN = 11
const HEAD_HALF = 4.4

export function buildFigure(a: FigureInput, view: FigureView): Figure | null {
  const x1 = view.xOfTime(a.start.time)
  const x2 = view.xOfTime(a.end.time)
  if (x1 === undefined || x2 === undefined) return null // 端点指向的 K 线不在已揭示数据里
  const y1 = view.yOfPrice(a.start.price)
  const y2 = view.yOfPrice(a.end.price)
  const style = resolveStyle(a)
  const dash = dashArray(style.dash)

  const figure: Figure = {
    id: a.id,
    kind: a.kind,
    segs: [],
    box: null,
    head: null,
    chips: [],
    anchors: [
      { x: x1, y: y1 },
      { x: x2, y: y2 },
    ],
  }

  switch (a.kind) {
    case 'rect': {
      figure.box = {
        x: Math.min(x1, x2),
        y: Math.min(y1, y2),
        w: Math.abs(x2 - x1),
        h: Math.abs(y2 - y1),
        color: style.color,
        width: style.width,
        dash,
        fill: style.fill ?? 'transparent',
        fillOpacity: style.fillOpacity ?? 0.12,
      }
      return figure
    }
    case 'fib': {
      // 四条水平线：比例 0 = 起点价格、1 = 终点价格、-1/2 是往外各一个区间。
      // 它们**永远水平、互相平行** —— 用户只能改颜色，不能单独拖某一条（需求 3.3）。
      const left = Math.min(x1, x2)
      const right = Math.max(x1, x2)
      const levels = style.levels ?? defaultLevels()
      const delta = a.end.price - a.start.price
      for (const lv of levels) {
        const price = a.start.price + lv.ratio * delta
        const y = view.yOfPrice(price)
        figure.segs.push({
          x1: left,
          y1: y,
          x2: right,
          y2: y,
          color: lv.color,
          width: style.width,
          dash,
          opacity: 1,
        })
        figure.chips.push({ x: right - 4, y: y - 4, text: `${lv.ratio}  ${view.priceText(price)}`, color: lv.color, anchor: 'end' })
      }
      return figure
    }
    case 'ray': {
      const end = clipRay(x1, y1, x2, y2, view.plot)
      figure.segs.push({ x1, y1, x2: end.x, y2: end.y, color: style.color, width: style.width, dash, opacity: 1 })
      return figure
    }
    case 'arrow': {
      const head = arrowHead(x1, y1, x2, y2, style.color)
      figure.head = head
      // 线画到箭头根部（再往前的部分由三角形占着）
      const stop = shorten(x1, y1, x2, y2, head ? HEAD_LEN * 0.7 : 0)
      figure.segs.push({ x1, y1, x2: stop.x, y2: stop.y, color: style.color, width: style.width, dash, opacity: 1 })
      return figure
    }
    default: {
      // trend（以及任何未知类型都按一条直线画，不会被静默丢掉）
      figure.segs.push({ x1, y1, x2, y2, color: style.color, width: style.width, dash, opacity: 1 })
      return figure
    }
  }
}

/** 从 (x2,y2) 沿 (x1,y1)->(x2,y2) 的方向退回 len 像素 */
function shorten(x1: number, y1: number, x2: number, y2: number, len: number): { x: number; y: number } {
  const dx = x2 - x1
  const dy = y2 - y1
  const dist = Math.hypot(dx, dy)
  if (len <= 0 || dist < 1e-6) return { x: x2, y: y2 }
  const back = Math.min(len, dist)
  return { x: x2 - (dx / dist) * back, y: y2 - (dy / dist) * back }
}

function arrowHead(x1: number, y1: number, x2: number, y2: number, color: string): Head | null {
  const dx = x2 - x1
  const dy = y2 - y1
  const dist = Math.hypot(dx, dy)
  if (dist < 2) return null // 太短了，画出来只是一坨
  const ux = dx / dist
  const uy = dy / dist
  const bx = x2 - ux * HEAD_LEN
  const by = y2 - uy * HEAD_LEN
  const px = -uy * HEAD_HALF
  const py = ux * HEAD_HALF
  return {
    points: `${x2},${y2} ${bx + px},${by + py} ${bx - px},${by - py}`,
    color,
  }
}

/**
 * 把「从 p1 出发、穿过 p2」的射线裁到价格区里，返回它离开这块区域前最后一个点。
 *
 * 斜率是按**当前视图**算的：横轴缩放和纵轴缩放都会改变屏幕上这条线的角度，
 * 所以每次重绘都要重算（这正是它必须留在前端的原因）。
 *
 * 射线往左画（终点在起点左边）也支持：往哪个方向延伸看的是 dx 的符号，不是「往右」。
 */
function clipRay(
  x1: number,
  y1: number,
  x2: number,
  y2: number,
  plot: { left: number; top: number; right: number; bottom: number },
): { x: number; y: number } {
  const dx = x2 - x1
  const dy = y2 - y1
  if (Math.abs(dx) < 1e-6 && Math.abs(dy) < 1e-6) return { x: x2, y: y2 }
  let t = Number.POSITIVE_INFINITY
  if (dx > 1e-9) t = Math.min(t, (plot.right - x1) / dx)
  if (dx < -1e-9) t = Math.min(t, (plot.left - x1) / dx)
  if (dy > 1e-9) t = Math.min(t, (plot.bottom - y1) / dy)
  if (dy < -1e-9) t = Math.min(t, (plot.top - y1) / dy)
  if (!Number.isFinite(t) || t < 1) return { x: x2, y: y2 }
  return { x: x1 + dx * t, y: y1 + dy * t }
}
