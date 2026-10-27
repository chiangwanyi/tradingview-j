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

/**
 * ATR 副图的高度（不开 ATR 时这块地方归价格区）。
 *
 * 给一个**比例 + 上下限**而不是写死 76px：画布在高分屏和小窗口下高度差很多，
 * 写死了在小窗口里会挤掉一半 K 线，在大屏上又细得像条缝。
 */
export const ATR_PANE_RATIO = 0.18
export const ATR_PANE_MIN = 56
export const ATR_PANE_MAX = 110

/**
 * 标注端点最多能向右延伸多少根 —— 和后端 instance.go 的 maxForwardBars 是同一个数。
 *
 * 前端夹一次是为了「预览 == 定稿」（后端也会夹，两边夹出同一个结果才不会点完就跳）；
 * 后端再夹一次是因为它不能信任消息内容。
 */
export const MAX_FORWARD_OFFSET = 5000

/**
 * 端点的横坐标：一根**真实存在**的 K 线 + 往右延伸的根数。
 *
 * 不是「一个时间字符串」也不是「一个像素」：像素不持久，未来时间不可靠（见 barRefAt）。
 */
export interface BarRef {
  /** 锚点 K 线的时间，一定是 bars 里真实存在的一根 */
  time: string
  /** 从锚点往右延伸的根数，0 = 就在这根 K 线上 */
  offset: number
}

export interface ChartLayout {
  width: number
  height: number
  /** 价格区（K 线、EMA、挂单线都画在这里） */
  plot: { left: number; top: number; right: number; bottom: number; width: number; height: number }
  /**
   * ATR 副图：价格区下方、时间轴上方的一条独立区域（关掉 ATR 时是 null）。
   *
   * 为什么要独立一块而不是叠在价格区上：ATR 的量纲就是价格（MNQ 上通常 20–60 点），
   * 和 30000 的报价放在同一个纵轴里只会被压成一条贴底的直线。
   *
   * 它**不是行情区**：右键下单、画标注这些动作在副图里要挡住（见 ChartPanel 的 `inAtrPane`）。
   */
  atr: { top: number; bottom: number; height: number } | null
  /** 底部时间轴（不画任何行情，只放时间标签） */
  timeAxis: { top: number; height: number }
}

export function computeLayout(width: number, height: number, showAtr = false): ChartLayout {
  const left = PLOT_LEFT
  const right = Math.max(left + 60, width - PRICE_AXIS_WIDTH)
  const timeTop = Math.max(PLOT_TOP + 40, height - TIME_AXIS_HEIGHT)

  // 副图从时间轴往上切。没空间时（窗口被压得很扁）宁可不给副图，
  // 也不要把价格区挤成负数 —— 那时 `plot.height` 是 1px，K 线会整片消失。
  let atrHeight = 0
  if (showAtr) {
    const wanted = Math.round(height * ATR_PANE_RATIO)
    atrHeight = Math.min(ATR_PANE_MAX, Math.max(ATR_PANE_MIN, wanted))
    if (timeTop - atrHeight - PLOT_TOP < 120) atrHeight = 0
  }
  const bottom = timeTop - atrHeight

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
    atr: atrHeight > 0 ? { top: bottom, bottom: timeTop, height: atrHeight } : null,
    timeAxis: { top: timeTop, height: TIME_AXIS_HEIGHT },
  }
}

export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v))
}

/**
 * 画布的坐标换算与布局：价格轴、横轴下标、可见区间、ATR 副图的地盘。
 * 绘制（render.ts）、标注图层、指针命中全都从这里取像素，口径只有一份。
 *
 * ATR：v0.23.0 起它的**值**来自 `chart/atr.ts`（前端按 Length / Smoothing 现算），
 * 不再读协议里的字段 —— 后端那套写死的 ATR14 已经删掉。这一层只负责「按下标取值、
 * 按可见区间贴合纵轴」，见下面的 `setAtrSeries` / `atrAt` / `fitAtr`。
 */
export class ChartScale {
  width = 0
  height = 0
  layout: ChartLayout = computeLayout(0, 0)
  bars: Bar[] = []
  /** 和 `bars` 一一对应的 ATR 序列（没有值的下标是 NaN），由 `setAtrSeries` 灌进来 */
  atrValues: number[] = []

  /** 一根 K 线占多少像素 */
  barWidth = 7
  /** 左边缘对应的（分数）下标 */
  scroll = 0
  priceMin = 0
  priceMax = 1
  /** 价格轴是否自动贴合可见区间；用户纵向拖动/Shift 滚轮后转为手动 */
  autoPrice = true
  /** ATR 副图是否占位（关掉时这块地方还给价格区） */
  atrOn = false
  /** ATR 副图自己的纵轴，左右边界由 `fitAtr()` 按可见区间算 */
  atrMin = 0
  atrMax = 1

  private indexOf = new Map<string, number>()

  /**
   * 容器尺寸变了（窗口缩放、工具栏多出一行、开关 ATR 副图……）。
   *
   * 布局当然要重算，但**用户自己调好的价格取景不能跟着丢**：
   *   - 手动缩过价格轴（`autoPrice === false`）时，「一个价格占多少像素」是他的成果。
   *     以前 ChartPanel 在尺寸变化时无条件 `fitPrice(true)`，于是「点一下图形」
   *     （工具条多出编辑模板条 → 画布矮了 27px）就把价格轴弹回自动贴合，
   *     看起来就是「画布缩放被重置了」。
   *   - 这里的做法：按**旧高度**记住每像素多少价格，再按新高度铺回去；顶边的价格
   *     （就是 `priceMax`，因为 `plot.top` 是常数）保持不动。于是视野上半部分
   *     一个像素都不移，变矮只是「下方少看见一段」。
   *
   * 自动模式（`autoPrice === true`）什么都不做：贴合可见区间是它的本职，交给 `fitPrice()`。
   */
  setSize(width: number, height: number): void {
    const keepMax = this.priceMax
    const span = this.priceMax - this.priceMin
    const oldPlotHeight = this.plotHeight
    this.width = width
    this.height = height
    this.layout = computeLayout(width, height, this.atrOn)
    if (!this.autoPrice && oldPlotHeight > 1 && span > 0) {
      this.priceMax = keepMax
      this.priceMin = keepMax - (span / oldPlotHeight) * this.plotHeight
    }
  }

  /**
   * 开关 ATR 副图。
   *
   * 必须**重算 layout**：副图占的是价格区的地盘，`plot.height` 一变，
   * yOf / priceAtY / 标注图层全都得跟着变。只翻一个布尔值不重算，
   * 画出来就是「K 线还是原来那么高，ATR 压在下面一截上」。
   */
  setAtrPane(on: boolean): void {
    if (this.atrOn === on) return
    this.atrOn = on
    this.layout = computeLayout(this.width, this.height, on)
  }

  setBars(bars: Bar[]): void {
    this.bars = bars
    this.indexOf.clear()
    for (let i = 0; i < bars.length; i += 1) this.indexOf.set(bars[i].time, i)
  }

  /**
   * 换一份 ATR 序列（改参数、多了一根、换了实例都要换）。
   *
   * 传进来的数组必须和 `bars` **按下标对齐**：两头都靠下标取数，错一格就是整条线平移一格，
   * 而且肉眼看不太出来 —— 只有和 K 线高低点对不上时才会觉得「这线怪怪的」。
   */
  setAtrSeries(values: number[]): void {
    this.atrValues = values
  }

  /** 第 i 根 K 线的 ATR；没有值（na / 越界 / 还没算）返回 null。 */
  atrAt(i: number): number | null {
    const v = this.atrValues[i]
    return typeof v === 'number' && Number.isFinite(v) ? v : null
  }

  /**
   * 时间 -> 下标。
   *
   * 先精确命中（`bars` 的时间是唯一键）。精确找不到时，退一步取「时间不晚于它的
   * 最后一根」—— 这是**聚合周期**要的语义：成交 / 标注锚的永远是 5 分钟时刻，
   * 而更大周期里它们落在某一个桶里（桶的 `time` 是桶内第一根的真实时间），
   * 不退这一步，切成 15 分钟后成交三角和标注就全都不见了。
   *
   * 比最早的 K 线还早的时间仍然返回 undefined（那是「这根不存在」，不该硬贴到第一根上）。
   *
   * shortcut: 5 分钟视图里若某个时刻真的缺根（数据被清理过），会画到前一根上而不是消失，
   * 需要区分「缺根」和「聚合成桶」时再引入显式判断。
   */
  indexOfTime(time: string): number | undefined {
    const exact = this.indexOf.get(time)
    if (exact !== undefined) return exact
    // 时间文本是定长的 "yyyy-MM-dd HH:MM"，字典序就是时间序
    let lo = 0
    let hi = this.bars.length - 1
    let found = -1
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (this.bars[mid].time <= time) {
        found = mid
        lo = mid + 1
      } else {
        hi = mid - 1
      }
    }
    return found >= 0 ? found : undefined
  }

  /**
   * 端点（锚点时间 + 向右偏移）-> 连续下标。
   *
   * 和 `indexOfTime` 的区别：这里**允许越过最后一根**（画到右侧空白区），所以不夹到
   * 数据范围里。锚点不在数据里（旧存档指向已删除的 K 线）返回 undefined，图形就不画。
   */
  virtualIndexOf(ref: { time: string; offset?: number }): number | undefined {
    const idx = this.indexOfTime(ref.time)
    if (idx === undefined) return undefined
    return idx + clamp(ref.offset ?? 0, 0, MAX_FORWARD_OFFSET)
  }

  /**
   * 连续下标 -> 端点。
   *
   * 这是「端点可以自由越过最后一根」和「严格时间价格坐标」的交点：
   *   - 下标落在一根已揭示的 K 线上 -> 存那根的**时间**，offset = 0（和以前完全一样）；
   *   - 下标越过最后一根 -> 锚在**最后一根**上，用 offset 记住「再往右几根」。
   *
   * 为什么不存一个外推出来的未来时间：休市/周末时外推出来的并不是真实 K 线的时间，
   * 而且等新 K 线揭示出来，同一个时间会落到另一个下标上 —— 画好的线会自己左右跳。
   * 「最后一根 + 根数」只依赖已经存在的 K 线，缩放、平移、步进都不会让它漂。
   *
   * 下标还会被夹在 [0, last + MAX_FORWARD_OFFSET]：左边没有「更早的 K 线」这个概念，
   * 右边留一个够画完整个屏幕的上限（见 MAX_FORWARD_OFFSET）。
   */
  barRefAt(index: number): BarRef | null {
    const last = this.bars.length - 1
    if (last < 0) return null
    const i = Math.round(index)
    if (i <= 0) return { time: this.bars[0].time, offset: 0 }
    if (i <= last) return { time: this.bars[i].time, offset: 0 }
    return { time: this.bars[last].time, offset: clamp(i - last, 1, MAX_FORWARD_OFFSET) }
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

  /**
   * 光标（横向像素）指向的那根 K 线，连带它的端点坐标。
   *
   * 为什么要有这一个：**光标上显示的吸附点，必须就是单击后落下的位置**。
   * 光标那条路要的是「哪根 K 线」（拿去挑 OHLC），绘图那条路要的是
   * 「锚点 + 向右几根」（拿去存坐标），两处各算一遍迟早会漂出半根。
   * 这里一次算完，谁要用哪部分就取哪部分。
   *
   * 横向夹在行情区左右两条边上（右边是价格轴，不是行情）：往右拖到最后一根之外时，
   * 下标会越过末尾，但返回的 `bar` 是**被夹回来的那根**（最后一根）—— 那一段没有 K 线，
   * 吸附只能吸在最后一根上（和后端 resolvePoint 一致）。
   */
  barAtCursor(x: number): { ref: BarRef; index: number; bar: Bar } | null {
    if (this.bars.length === 0) return null
    const { plot } = this.layout
    const ref = this.barRefAt(this.indexAtX(clamp(x, plot.left, plot.right)))
    if (!ref) return null
    const vi = this.virtualIndexOf(ref)
    if (vi === undefined) return null
    const index = clamp(vi, 0, this.bars.length - 1)
    const bar = this.bars[index]
    if (!bar) return null
    return { ref, index, bar }
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

  /**
   * ATR 副图纵轴贴合可见区间。
   *
   * 和价格轴不同，**没有手动模式**：副图上不存在任何拖拽/缩放的手势，
   * 一直是自动的，所以每次重绘都跟着可见区间重算（不缓存，缓存了平移一下就过期）。
   */
  fitAtr(): void {
    const atr = this.layout.atr
    if (!atr) return
    const { from, to } = this.visibleRange()
    let lo = Number.POSITIVE_INFINITY
    let hi = Number.NEGATIVE_INFINITY
    for (let i = from; i <= to; i += 1) {
      const v = this.atrAt(i)
      if (v === null) continue
      if (v < lo) lo = v
      if (v > hi) hi = v
    }
    if (!Number.isFinite(lo) || !Number.isFinite(hi)) {
      this.atrMin = 0
      this.atrMax = 1
      return
    }
    const span = hi - lo
    // 全段 ATR 完全相同（横盘）时 span 是 0：给一个按数值大小取的余量，
    // 否则上下界相等，`atrYOf` 会把整条线画在同一个像素上（看起来像没画出来）。
    const pad = span > 0 ? span * 0.12 : Math.max(hi * 0.15, 0.5)
    this.atrMin = Math.max(0, lo - pad)
    this.atrMax = hi + pad
  }

  /** ATR 值 -> 副图里的像素 y */
  atrYOf(v: number): number {
    const atr = this.layout.atr
    if (!atr) return this.layout.plot.bottom
    const span = this.atrMax - this.atrMin
    if (!(span > 0)) return atr.bottom
    return atr.top + ((this.atrMax - v) / span) * atr.height
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
