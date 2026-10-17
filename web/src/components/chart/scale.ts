import type { Bar } from '@/types/protocol'

/**
 * 画布的坐标换算。**所有像素 <-> 数据坐标的换算只在这里做**。
 *
 * 数据坐标只有两种：
 *   - 横轴：K 线下标（整数），再通过下标拿到 CSV 里的时间字符串；
 *   - 纵轴：价格。
 *
 * 缩放/平移是**唯一**留在前端的操作（需求明确豁免），所以这里的 scroll / barWidth
 * 不上报后端；但标注的最终坐标一律是「时间字符串 + 价格」，由后端校验后广播回来。
 */

export const PRICE_AXIS_WIDTH = 86
export const TIME_AXIS_HEIGHT = 24
export const PLOT_TOP = 10
export const PLOT_LEFT = 10

export interface ChartLayout {
  width: number
  height: number
  /** 价格区（K 线、EMA、挂单线都画在这里） */
  plot: { left: number; top: number; right: number; bottom: number; width: number; height: number }
  /** 底部时间轴（不画任何行情，只放时间标签） */
  timeAxis: { top: number; height: number }
}

export function computeLayout(width: number, height: number): ChartLayout {
  const left = PLOT_LEFT
  const right = Math.max(left + 60, width - PRICE_AXIS_WIDTH)
  const bottom = Math.max(PLOT_TOP + 40, height - TIME_AXIS_HEIGHT)
  return {
    width,
    height,
    plot: {
      left,
      top: PLOT_TOP,
      right,
      bottom,
      width: Math.max(1, right - left),
      height: Math.max(1, bottom - PLOT_TOP),
    },
    timeAxis: { top: bottom, height: TIME_AXIS_HEIGHT },
  }
}

export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v))
}

export class ChartScale {
  width = 0
  height = 0
  layout: ChartLayout = computeLayout(0, 0)
  bars: Bar[] = []

  /** 一根 K 线占多少像素 */
  barWidth = 7
  /** 左边缘对应的（分数）下标 */
  scroll = 0
  priceMin = 0
  priceMax = 1
  /** 价格轴是否自动贴合可见区间；用户纵向拖动/Shift 滚轮后转为手动 */
  autoPrice = true

  private indexOf = new Map<string, number>()

  setSize(width: number, height: number): void {
    this.width = width
    this.height = height
    this.layout = computeLayout(width, height)
  }

  setBars(bars: Bar[]): void {
    this.bars = bars
    this.indexOf.clear()
    for (let i = 0; i < bars.length; i += 1) this.indexOf.set(bars[i].time, i)
  }

  indexOfTime(time: string): number | undefined {
    return this.indexOf.get(time)
  }

  get plotWidth(): number {
    return this.layout.plot.width
  }

  get plotHeight(): number {
    return this.layout.plot.height
  }

  get visibleBarSpan(): number {
    return Math.max(1, this.plotWidth / this.barWidth)
  }

  xOf(index: number): number {
    return this.layout.plot.left + (index - this.scroll + 0.5) * this.barWidth
  }

  /** 像素 -> 分数下标（可能是 .5，落在两根之间） */
  indexAtX(x: number): number {
    return this.scroll + (x - this.layout.plot.left) / this.barWidth - 0.5
  }

  /** 像素 -> 最近的 K 线下标（超出范围会夹到两端） */
  nearestIndex(x: number): number {
    if (this.bars.length === 0) return 0
    return clamp(Math.round(this.indexAtX(x)), 0, this.bars.length - 1)
  }

  yOf(price: number): number {
    const span = this.priceMax - this.priceMin
    if (span <= 0) return this.layout.plot.bottom
    const ratio = (this.priceMax - price) / span
    return this.layout.plot.top + ratio * this.plotHeight
  }

  priceAtY(y: number): number {
    const ratio = (y - this.layout.plot.top) / this.plotHeight
    return this.priceMax - ratio * (this.priceMax - this.priceMin)
  }

  /** 当前可见的 K 线下标范围（闭区间，已夹到数据范围内） */
  visibleRange(): { from: number; to: number } {
    if (this.bars.length === 0) return { from: 0, to: 0 }
    const from = clamp(Math.floor(this.scroll), 0, this.bars.length - 1)
    const to = clamp(Math.ceil(this.scroll + this.visibleBarSpan), 0, this.bars.length - 1)
    return { from, to }
  }

  /** 价格轴贴合可见区间（autoPrice 为 false 时只有 force 才会动） */
  fitPrice(force = false): void {
    if (!this.autoPrice && !force) return
    const { from, to } = this.visibleRange()
    let lo = Number.POSITIVE_INFINITY
    let hi = Number.NEGATIVE_INFINITY
    for (let i = from; i <= to; i += 1) {
      const b = this.bars[i]
      if (!b) continue
      if (b.low < lo) lo = b.low
      if (b.high > hi) hi = b.high
    }
    if (!Number.isFinite(lo) || !Number.isFinite(hi)) return
    const span = hi - lo
    const pad = span > 0 ? span * 0.08 : Math.max(Math.abs(hi) * 0.002, 0.5)
    this.priceMin = lo - pad
    this.priceMax = hi + pad
  }

  /** 以某个像素位置为锚点缩放时间轴 */
  zoomTime(factor: number, anchorX: number): void {
    const idx = this.indexAtX(anchorX)
    this.barWidth = clamp(this.barWidth * factor, 2, 40)
    this.scroll = idx + 0.5 - (anchorX - this.layout.plot.left) / this.barWidth
    this.clampScroll()
    this.fitPrice()
  }

  /** 以某个像素位置为锚点缩放价格轴（自动转为手动价格轴） */
  zoomPrice(factor: number, anchorY: number): void {
    const p = this.priceAtY(anchorY)
    this.priceMin = p + (this.priceMin - p) * factor
    this.priceMax = p + (this.priceMax - p) * factor
    this.autoPrice = false
  }

  /** 拖动平移：dx 像素横向，dy 像素纵向 */
  panBy(dx: number, dy: number): void {
    this.scroll -= dx / this.barWidth
    this.clampScroll()
    if (Math.abs(dy) > 0) {
      const delta = (dy / this.plotHeight) * (this.priceMax - this.priceMin)
      this.priceMin += delta
      this.priceMax += delta
      this.autoPrice = false
    }
    this.fitPrice()
  }

  clampScroll(): void {
    const minScroll = -this.visibleBarSpan * 0.4
    const maxScroll = Math.max(minScroll, this.bars.length - 1)
    this.scroll = clamp(this.scroll, minScroll, maxScroll)
  }

  /**
   * 让最后一根出现在右侧偏内一点的位置。
   *
   * **只给 `resetView()` 用**（首次进入实例 / 换实例时的初始取景）。
   * 加载新 K 线时绝不能调它：那会把整屏 K 线向左推，用户正在看的历史就跑掉了。
   */
  scrollToTail(): void {
    this.scroll = this.bars.length - 1 - this.visibleBarSpan * 0.88
    this.clampScroll()
  }

  /** 重置视图：合适的时间密度 + 自动价格轴 + 贴住末端（换实例才用，加载新 K 线不用） */
  resetView(): void {
    const targetBars = 140
    this.barWidth = clamp(this.plotWidth / targetBars, 3, 16)
    this.autoPrice = true
    this.scrollToTail()
    this.fitPrice(true)
  }

  resetPrice(): void {
    this.autoPrice = true
    this.fitPrice(true)
  }

  /**
   * 时间轴刻度间隔（多少根一个标签）。
   *
   * 标签现在是完整日期时间「周五 2026-10-16 09:35」，比以前的「09:35」长一倍多，
   * 所以留白从 96px 抬到 180px —— 否则相邻两个标签会叠在一起。
   */
  timeTickStep(): number {
    const wanted = Math.ceil(180 / this.barWidth)
    for (const step of [1, 2, 3, 4, 6, 12, 24, 48, 96, 288, 576]) {
      if (step >= wanted) return step
    }
    return 576
  }
}

/** 1 / 2 / 5 序列里挑一个「够大」的步长，保证标签是好读的数 */
function niceStep(raw: number): number {
  const mag = 10 ** Math.floor(Math.log10(raw))
  const norm = raw / mag
  if (norm >= 5) return 10 * mag
  if (norm >= 2) return 5 * mag
  if (norm >= 1) return 2 * mag
  return mag
}

/**
 * 价格轴刻度。
 *
 * tick > 0 时步长会被抬到它的整数倍：MNQ（0.25 一跳）的刻度就是 0.25 / 0.5 / 1，
 * 不会出现 0.3 这种在这个标的上根本不存在的价格。
 */
export function niceTicks(min: number, max: number, count: number, tick = 0): number[] {
  const span = max - min
  if (!(span > 0) || !Number.isFinite(span)) return [min]
  let step = niceStep(span / count)
  if (tick > 0 && step > tick) {
    step = Math.ceil(step / tick - 1e-9) * tick
  }
  const first = Math.ceil(min / step) * step
  const out: number[] = []
  for (let v = first; v <= max + step * 0.001 && out.length < 40; v += step) {
    out.push(Math.abs(v) < step * 1e-9 ? 0 : roundTo(v, decimalsForTick(tick)))
  }
  return out
}

function roundTo(v: number, decimals: number): number {
  const f = 10 ** decimals
  return Math.round(v * f) / f
}

/**
 * 最小 tick 需要几位小数。
 *
 * 下限是 2：加载时价格已经四舍五入到两位小数（89.97000000000001 -> 89.97），
 * 只留一位会把 89.97 显示成 90.0，反而看不出真实报价。
 */
export function decimalsForTick(tick: number): number {
  if (!(tick > 0)) return 2
  const needed = Math.ceil(-Math.log10(tick) - 1e-9)
  return clamp(Math.max(2, needed), 2, 6)
}

export function formatPrice(v: number, decimals = 2): string {
  return v.toFixed(decimals)
}

export function formatTick(tick: number): string {
  return tick.toFixed(decimalsForTick(tick)).replace(/0+$/, '').replace(/\.$/, '')
}

/**
 * 把任意价格吸附到「这个标的上真实存在的报价」。
 *
 * MES 一跳 0.25，那么光标只能落在 6484.00 / 6484.25 / 6484.50 / 6484.75，
 * 绝不会出现 6484.12 —— 鼠标停在两根刻度之间时，取**最近**的那个合法价。
 *
 * 没拿到 tick（还没进实例）时退化为两位小数，和加载时的四舍五入保持同一把尺子。
 */
export function snapToTick(price: number, tick: number): number {
  if (!(tick > 0)) return roundTo(price, 2)
  return roundTo(Math.round(price / tick) * tick, decimalsForTick(tick))
}

const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'] as const

/**
 * "2026-10-16 09:35" -> "周五 2026-10-16 09:35"。
 *
 * 星期几用**本地日历**算（年/月/日直接喂给 Date，不经过时区换算），
 * 所以不依赖字符串里的时间部分，也不会因为 UTC 偏移把日期算前一天。
 */
export function formatAxisTime(time: string): string {
  const y = Number(time.slice(0, 4))
  const m = Number(time.slice(5, 7))
  const d = Number(time.slice(8, 10))
  if (!(y > 1970) || !(m >= 1 && m <= 12) || !(d >= 1 && d <= 31)) return time
  const wd = WEEKDAYS[new Date(y, m - 1, d).getDay()]
  return wd ? `${wd} ${time}` : time
}

export function formatSigned(v: number, decimals = 2): string {
  const s = v.toFixed(decimals)
  return v > 0 ? `+${s}` : s
}
