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

export const PRICE_AXIS_WIDTH = 66
export const TIME_AXIS_HEIGHT = 22
export const PLOT_TOP = 12
export const PLOT_LEFT = 10
export const PANEL_HEIGHT = 52
export const PANEL_GAP = 10

export interface ChartLayout {
  width: number
  height: number
  /** 价格区 */
  plot: { left: number; top: number; right: number; bottom: number; width: number; height: number }
  /** BarCount 指标区 */
  panel: { left: number; top: number; right: number; bottom: number; width: number; height: number }
}

export function computeLayout(width: number, height: number): ChartLayout {
  const left = PLOT_LEFT
  const right = Math.max(left + 60, width - PRICE_AXIS_WIDTH)
  const panelBottom = Math.max(PLOT_TOP + 90, height - TIME_AXIS_HEIGHT)
  const panelTop = panelBottom - PANEL_HEIGHT
  const plotTop = PLOT_TOP
  const plotBottom = Math.max(plotTop + 40, panelTop - PANEL_GAP)
  return {
    width,
    height,
    plot: {
      left,
      top: plotTop,
      right,
      bottom: plotBottom,
      width: Math.max(1, right - left),
      height: Math.max(1, plotBottom - plotTop),
    },
    panel: {
      left,
      top: panelTop,
      right,
      bottom: panelBottom,
      width: Math.max(1, right - left),
      height: Math.max(1, panelBottom - panelTop),
    },
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

  /** 让最后一根出现在右侧偏内一点的位置 */
  scrollToTail(): void {
    this.scroll = this.bars.length - 1 - this.visibleBarSpan * 0.88
    this.clampScroll()
  }

  /** 重置视图：合适的时间密度 + 自动价格轴 + 贴住末端 */
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

  /** 时间轴刻度间隔（多少根一个标签），按「标签至少 96px 不重叠」来挑 */
  timeTickStep(): number {
    const wanted = Math.ceil(96 / this.barWidth)
    for (const step of [1, 2, 3, 4, 6, 12, 24, 48, 96, 288, 576]) {
      if (step >= wanted) return step
    }
    return 576
  }

  /** BarCount 指标区的最大值（可见范围内） */
  panelMax(): number {
    const { from, to } = this.visibleRange()
    let max = 1
    for (let i = from; i <= to; i += 1) {
      const b = this.bars[i]
      if (b && b.barCount > max) max = b.barCount
    }
    return Math.ceil(max * 1.08)
  }

  yOfBarCount(v: number, max: number): number {
    const h = this.layout.panel.height
    return this.layout.panel.bottom - (v / Math.max(1, max)) * h
  }
}

/** 价格轴刻度：1/2/5 序列，保证标签是好读的数 */
export function niceTicks(min: number, max: number, count: number): number[] {
  const span = max - min
  if (!(span > 0) || !Number.isFinite(span)) return [min]
  const raw = span / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const norm = raw / mag
  let step: number
  if (norm >= 5) step = 10 * mag
  else if (norm >= 2) step = 5 * mag
  else if (norm >= 1) step = 2 * mag
  else step = mag
  const first = Math.ceil(min / step) * step
  const out: number[] = []
  for (let v = first; v <= max + step * 0.001 && out.length < 40; v += step) {
    out.push(Math.abs(v) < step * 1e-9 ? 0 : v)
  }
  return out
}

/** 根据刻度间隔决定显示几位小数（0.25 一跳的期货要两位，整数报价不要小数） */
export function decimalsForStep(step: number): number {
  if (!(step > 0)) return 2
  const d = Math.ceil(-Math.log10(step))
  return clamp(d, 0, 4)
}

export function formatPrice(v: number, decimals = 2): string {
  return v.toFixed(decimals)
}

export function formatSigned(v: number, decimals = 2): string {
  const s = v.toFixed(decimals)
  return v > 0 ? `+${s}` : s
}
