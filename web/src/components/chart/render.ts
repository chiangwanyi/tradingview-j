import type { Order, Position, Trade } from '@/types/protocol'

import { atrLegend, type AtrSettings } from './atr'
import { barCountMarks, type BarCountSettings } from './barcount'
import { indicatorSeries, type IndicatorSettings } from './indicators'
import { snapAtCursor, type OHLCItem } from './snap'
import type { ClosedTrade } from './trades'
import {
  ChartScale,
  clamp,
  decimalsForTick,
  formatAxisTime,
  formatPrice,
  formatSigned,
  formatTick,
  niceTicks,
  snapToTick,
  type ChartLayout,
} from './scale'

/**
 * Canvas 上画的东西：网格、坐标轴、K 线、EMA20、ATR 副图、Doji X + Blue ZigZag、
 * Bar Count（K 线下方的当天第几根）、挂单价线、十字光标。
 *
 * 标注不在这里画 —— 标注是 SVG 图层（便于命中测试与拖动），见 ChartPanel.vue。
 * 两层的坐标换算都来自同一个 ChartScale，不会出现「图形和 K 线对不上」。
 *
 * **只有一套配色（浅色）**，分两层：
 *
 *   1. K 线保持**黑白**（TradingView 浅色的经典画法）：
 *        阳线 —— 白填充 + 黑边框 / 黑影线（空心）
 *        阴线 —— 全黑（实心）
 *      空心 / 实心本身就是方向编码，不依赖颜色，打印出来、红绿色弱都分得清。
 *   2. 其余「有方向含义」的元素用美股语义色（绿 = 涨 / 做多 / 盈利，红 = 跌 / 做空 / 亏损）：
 *      挂单线、持仓成本线、成交三角、现价标签 —— 这些和 K 线叠在一起时，
 *      黑白反而会糊成一片，用颜色才分得开。
 *
 * 页面那边的 `--c-up` / `--c-down` 和第 2 类里的绿红是同一组数值，改颜色两处一起改。
 */

/** 一根 K 线的描边与填充分开给：靠「空心/实心」区分涨跌。 */
export interface CandleColors {
  stroke: string
  fill: string
}

export interface ChartTheme {
  bg: string
  grid: string
  axisLine: string
  text: string
  textDim: string
  crosshair: string
  up: CandleColors
  down: CandleColors
  flat: CandleColors
  ema: string
  /** ATR 副图那条线：和 EMA（琥珀）、ZigZag（蓝）、Doji（近黑）都不撞 */
  atr: string
  /** Doji X + Blue ZigZag 的两个颜色：蓝点（Pine 的 color.blue）、✖（Pine 的 color.black） */
  zigzag: string
  /** 连蓝点的那条折线：同色但压低透明度，免得比 K 线还抢眼 */
  zigzagLine: string
  doji: string
  /** 现价标签（价格轴上的实心小块） */
  lastBoxBg: string
  lastBoxText: string
  /** 十字光标的价格标签 */
  crossBoxBg: string
  crossBoxText: string
  /** 挂单线 + 标签 */
  orderBuy: string
  orderSell: string
  orderBuyBox: string
  orderSellBox: string
  orderText: string
  /** 持仓成本线（多头 / 空头各一色）+ 标签 */
  posLong: string
  posShort: string
  posLongBox: string
  posShortBox: string
  posText: string
  /**
   * 白底上的「涨 / 跌」文字色（K 线数据栏的涨跌、挂单处的预估盈亏）。
   *
   * 不能直接拿 `up.stroke` / `down.stroke` 当文字色：K 线那两项是**黑白**的
   * （阳线黑边空心的画法决定的），拿来写字就跟正文一个颜色了。
   */
  textUp: string
  textDown: string
  /** 成交标记：买入三角 / 卖出三角 / 「开仓→平仓」连线 */
  tradeBuy: string
  tradeSell: string
  /** 选中标注的虚线框 */
  selection: string
  /** Ctrl 吸附生效时的高亮色：十字线、价格标签、OHLC 图例里被吸住的那一项 */
  snap: string
  /** 吸附高亮块上的文字色（琥珀底上不能用浅色字，对比度不够） */
  snapText: string
}

/**
 * 唯一的一套画布配色（浅色）。
 *
 * K 线是黑白的（阳线空心、阴线实心）；挂单 / 持仓 / 成交 / 现价标签用页面那套
 * 方向语义色：绿 = 涨 / 做多 / 盈利，红 = 跌 / 做空 / 亏损。
 */
export const CHART_THEME: ChartTheme = {
  bg: '#ffffff',
  grid: '#e8eef6',
  axisLine: '#cbd5e1',
  text: '#0f172a',
  textDim: '#475569',
  crosshair: '#64748b',
  // K 线：黑白配色。阳线黑边 + 白填充（空心），阴线全黑（实心）；
  // 十字星（flat）用中性灰，和「涨/跌」都不撞。
  up: { stroke: '#111827', fill: '#ffffff' },
  down: { stroke: '#111827', fill: '#111827' },
  flat: { stroke: '#6b7280', fill: '#ffffff' },
  ema: '#b45309',
  atr: '#7c3aed',
  zigzag: '#1d4ed8',
  zigzagLine: 'rgba(29, 78, 216, 0.5)',
  doji: '#0f172a',
  // 现价标签：黑底白字 —— 和 K 线同一套黑白，不跟涨跌染色（见 drawLastPrice）
  lastBoxBg: '#111827',
  lastBoxText: '#ffffff',
  crossBoxBg: '#475569',
  crossBoxText: '#ffffff',
  // 挂单：买入（做多方向）绿、卖出（做空方向）红 —— 和持仓线、成交三角同一套
  orderBuy: 'rgba(21, 128, 61, 0.85)',
  orderSell: 'rgba(185, 28, 28, 0.85)',
  orderBuyBox: 'rgba(21, 128, 61, 0.95)',
  orderSellBox: 'rgba(185, 28, 28, 0.95)',
  orderText: '#ffffff',
  posLong: 'rgba(21, 128, 61, 0.9)',
  posShort: 'rgba(185, 28, 28, 0.9)',
  posLongBox: '#15803d',
  posShortBox: '#b91c1c',
  posText: '#ffffff',
  // 涨跌语义色：和页面 `--c-up` / `--c-down`、持仓线、成交三角是同一组绿/红。
  // 全程序只有这两个方向色，画布上写数字也必须用它，否则会出现「两种绿」。
  textUp: '#15803d',
  textDown: '#b91c1c',
  tradeBuy: '#15803d',
  tradeSell: '#b91c1c',
  selection: '#334155',
  // 白底上压暗一档的琥珀：和涨跌红绿都不撞，一眼能认出这不是普通十字线
  snap: '#b45309',
  snapText: '#ffffff',
}

export interface HoverState {
  index: number
  x: number
  y: number
  inside: boolean
  /**
   * 指针此刻是否按着 Ctrl。
   *
   * 只传「按没按」，**不传吸到了哪个价**：吸附由十字光标每一帧自己算
   * （见 `drawCrosshair`），于是缩放、平移、步进之后它永远是对的。
   * 要是把结果缓存进这个对象，滚一下滚轮十字线就会停在上次算出的那个价位上，
   * 看着就像吸附失灵了。ChartPanel 里的价格提示也调同一个函数，两边必然一致。
   */
  snap?: boolean
}

export interface DrawInput {
  ctx: CanvasRenderingContext2D
  scale: ChartScale
  theme: ChartTheme
  hover: HoverState | null
  symbol: string
  orders: Order[]
  trades: Trade[]
  /** 已经配好对的「开仓 → 平仓」（见 chart/trades.ts），画连线用 */
  closed: ClosedTrade[]
  position: Position
  /** 浮动盈亏（后端按当前 K 线收盘价算好，美元） */
  unrealizedPnl: number
  /**
   * 每点值多少美元（= tickValue / tick）。
   *
   * 挂单旁边那句「这笔成交后能赚/亏多少」要用它换算 —— 画布只排版，不自己再推一遍
   * 「一跳值多少」，那一份口径在 store 里（和后端 `pointValueOf` 对齐）。
   */
  pointValue: number
  showEma: boolean
  /** ATR 副图的开关与参数（纯显示设置，值在 chart/atr.ts 里算；序列由 ChartPanel 灌进 scale） */
  atr: AtrSettings
  /** Doji X + Blue ZigZag 的开关与参数（纯显示设置，值在 chart/indicators.ts 里算） */
  indicator: IndicatorSettings
  /** Bar Count（K 线下方的「当天第几根」）：开关 + 颜色，编号口径在 chart/barcount.ts 里 */
  barCount: BarCountSettings
  /** 最小跳动，来自 _symbols.json（0.25 / 0.1 / 0.01…） */
  tick: number
  /**
   * 正在拖动 / 悬停的挂单 id。画粗一档、标签带上价格 —— 拖动时用户要看得见
   * 「现在落在哪个价」，那一路靠价格轴上的标签得眯着眼对。
   */
  activeOrderId?: string | null
  /** 悬停 / 钉住的平仓线（`closed` 里的下标）：加粗一点，让「你说的是哪一条」看得见 */
  activeClosed?: number | null
}

const MONO = '12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
const MONO_BOLD = '600 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
/** 价格轴/时间轴上那种实心小标签的高度（12px 字放进去不挤） */
const AXIS_LABEL_H = 18
/**
 * 挂单标签右端给「撤单 ×」让出的宽度。
 *
 * 那个 × 是 HTML 按钮，叠在画布上（见 ChartPanel.vue）—— 画布的拾取要自己算坐标，
 * 按钮却天然有 hover / title / disabled。两边共用一个常数，标签就不会顶到按钮底下。
 */
export const ORDER_RESERVE_RIGHT = 24

export function drawChart(input: DrawInput): void {
  const {
    ctx,
    scale,
    theme,
    hover,
    symbol,
    orders,
    trades,
    closed,
    position,
    unrealizedPnl,
    pointValue,
    showEma,
    atr,
    indicator,
    barCount,
    tick,
    activeOrderId = null,
    activeClosed = null,
  } = input
  const { width, height } = scale
  const layout: ChartLayout = scale.layout
  const decimals = decimalsForTick(tick)

  ctx.save()
  ctx.clearRect(0, 0, width, height)
  ctx.fillStyle = theme.bg
  ctx.fillRect(0, 0, width, height)

  if (scale.bars.length === 0) {
    ctx.fillStyle = theme.textDim
    ctx.font = MONO
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText('还没有数据：点右上角「标的与实例」建一个实例', width / 2, height / 2)
    ctx.restore()
    return
  }

  const priceTicks = niceTicks(scale.priceMin, scale.priceMax, 6, tick)

  drawGrid(ctx, scale, theme, priceTicks)
  // ATR 副图：独立一块，压在网格之上、K 线之下（两者不重叠，顺序只是习惯）
  if (atr.show) drawAtrPane(ctx, scale, theme, decimals, hover, atr)
  drawCandles(ctx, scale, theme, showEma)
  // 指标压在 K 线之上、成交标记之下：蓝点要落在影线尖端上，不能被后面的三角盖住
  drawIndicator(ctx, scale, theme, indicator)
  // Bar Count：数字吊在每根 K 线下方，只是标注，不和任何价格元素抢位置
  drawBarCount(ctx, scale, barCount, indicator)
  // 成交标记压在 K 线上面：复盘时要一眼看到「哪里开、哪里平」
  drawTradeMarks(ctx, scale, theme, trades, closed, activeClosed)
  drawOrders(ctx, scale, theme, orders, position, pointValue, activeOrderId, decimals)
  drawPositionLine(ctx, scale, theme, position, unrealizedPnl, decimals)
  drawLastPrice(ctx, scale, theme, decimals)
  drawAxes(ctx, scale, theme, priceTicks, decimals, tick)
  drawWatermark(ctx, layout, theme, symbol)
  if (hover && hover.inside) drawCrosshair(ctx, scale, theme, hover, decimals, tick)

  ctx.restore()
}

function drawGrid(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  ticks: number[],
): void {
  const { plot, timeAxis } = scale.layout
  ctx.strokeStyle = theme.grid
  ctx.lineWidth = 1
  ctx.beginPath()
  for (const v of ticks) {
    const y = Math.round(scale.yOf(v)) + 0.5
    if (y < plot.top || y > plot.bottom) continue
    ctx.moveTo(plot.left, y)
    ctx.lineTo(plot.right, y)
  }
  // 时间网格线（只画竖线，和标签同一个步长）。
  // 有 ATR 副图时一直画到副图底边：副图和 K 线共用横轴，断在中间会让「对齐」这件事看着不确定。
  const tickStep = scale.timeTickStep()
  const { from, to } = scale.visibleRange()
  const gridBottom = scale.layout.atr ? scale.layout.atr.bottom : plot.bottom
  const first = Math.ceil(from / tickStep) * tickStep
  for (let i = first; i <= to; i += tickStep) {
    const x = Math.round(scale.xOf(i)) + 0.5
    if (x < plot.left || x > plot.right) continue
    ctx.moveTo(x, plot.top)
    ctx.lineTo(x, gridBottom)
  }
  ctx.stroke()

  // 内容区（价格区，或开着 ATR 时的副图）与时间轴之间的分隔线
  ctx.strokeStyle = theme.axisLine
  const baseY = Math.round(timeAxis.top) + 0.5
  ctx.beginPath()
  ctx.moveTo(plot.left, baseY)
  ctx.lineTo(plot.right, baseY)
  ctx.stroke()
}

/**
 * ATR 副图（价格区下方一条独立区域）。
 *
 * 为什么不叠在价格区里：ATR 的量纲就是价格（MNQ 上通常 20–60 点），而报价是 30000 ——
 * 同一个纵轴下它会变成贴着底边的一条直线，什么都看不出来。
 *
 * 画法上的几条约定：
 *   - 纵轴**自动贴合可见区间**（`scale.fitAtr()`），没有手动缩放：副图用来判断「现在波动
 *     比刚才大还是小」，不像价格那样需要盯某个具体价位；
 *   - 值的来源是前端算好的 ATR 序列（见 chart/atr.ts，参数由用户拧），这里只管画；
 *   - 线在 na 处**断开**（前 length-1 根没有值），不补点，否则会凭空造出一段不存在的历史；
 *   - 左上角常驻一个数值：不悬停也要能读到「现在多大」，这正是看 ATR 的目的。
 */
function drawAtrPane(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  decimals: number,
  hover: HoverState | null,
  settings: AtrSettings,
): void {
  const atr = scale.layout.atr
  if (!atr) return
  const { plot } = scale.layout
  const { from, to } = scale.visibleRange()
  scale.fitAtr()

  ctx.save()

  // 与价格区之间的分界线
  ctx.strokeStyle = theme.axisLine
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(plot.left, Math.round(atr.top) + 0.5)
  ctx.lineTo(plot.right, Math.round(atr.top) + 0.5)
  ctx.stroke()

  // 中值参考线：只画一条。上界下界就是区域边界，再画线会跟边框重影。
  const mid = (scale.atrMax + scale.atrMin) / 2
  ctx.strokeStyle = theme.grid
  ctx.setLineDash([2, 4])
  ctx.beginPath()
  ctx.moveTo(plot.left, Math.round(scale.atrYOf(mid)) + 0.5)
  ctx.lineTo(plot.right, Math.round(scale.atrYOf(mid)) + 0.5)
  ctx.stroke()
  ctx.setLineDash([])

  // 折线
  ctx.strokeStyle = theme.atr
  ctx.lineWidth = 1.4
  ctx.beginPath()
  let started = false
  for (let i = from; i <= to; i += 1) {
    const v = scale.atrAt(i)
    if (v === null) {
      started = false
      continue
    }
    const x = scale.xOf(i)
    const y = scale.atrYOf(v)
    if (!started) {
      ctx.moveTo(x, y)
      started = true
    } else {
      ctx.lineTo(x, y)
    }
  }
  ctx.stroke()

  // 右侧刻度：只标上下界（中间那条参考线的值一眼能算出来，标了反而挤）
  ctx.font = MONO
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  ctx.fillStyle = theme.textDim
  ctx.fillText(formatPrice(scale.atrMax, decimals), plot.right + 5, atr.top + 8)
  ctx.fillText(formatPrice(scale.atrMin, decimals), plot.right + 5, atr.bottom - 8)

  // 左上角：图例 + 数值。悬停时显示光标那根的，不悬停显示最后一根（= 当前这根）。
  // 图例带上参数（`ATR 14 RMA`）：副图开着的窗口不止一屏，光看线条分不出这是哪一套参数。
  const shown = hover ? hover.index : scale.bars.length - 1
  const value = scale.atrAt(shown)
  const legend = atrLegend(settings)
  let lx = plot.left + 6
  ctx.textAlign = 'left'
  ctx.textBaseline = 'top'
  ctx.fillStyle = theme.textDim
  ctx.fillText(legend, lx, atr.top + 3)
  lx += ctx.measureText(legend).width + 4
  ctx.fillStyle = theme.atr
  ctx.fillText(value === null ? '—' : formatPrice(value, decimals), lx, atr.top + 3)

  ctx.restore()
}

function drawCandles(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  showEma: boolean,
): void {
  const { from, to } = scale.visibleRange()
  const bodyWidth = Math.max(1, Math.min(scale.barWidth * 0.72, 22))
  const half = bodyWidth / 2

  for (let i = from; i <= to; i += 1) {
    const b = scale.bars[i]
    if (!b) continue
    const x = scale.xOf(i)
    const up = b.close > b.open
    const down = b.close < b.open
    const c = up ? theme.up : down ? theme.down : theme.flat

    ctx.strokeStyle = c.stroke
    ctx.fillStyle = c.fill
    ctx.lineWidth = 1

    // 影线
    const yHigh = scale.yOf(b.high)
    const yLow = scale.yOf(b.low)
    const xCenter = Math.round(x) + 0.5
    ctx.beginPath()
    ctx.moveTo(xCenter, yHigh)
    ctx.lineTo(xCenter, yLow)
    ctx.stroke()

    // 实体：先填后描边。阳线「黑边 + 白填充」= 空心，阴线「黑边 + 黑填充」= 实心，
    // 靠空心/实心而不是颜色来编码方向（K 线保持黑白配色）。
    const yOpen = scale.yOf(b.open)
    const yClose = scale.yOf(b.close)
    const top = Math.min(yOpen, yClose)
    const h = Math.max(1, Math.abs(yClose - yOpen))
    if (bodyWidth <= 2.5) {
      // 太窄了：描边会把实体整个糊掉，直接填一格
      ctx.fillRect(Math.round(x), Math.round(top), Math.max(1, Math.round(bodyWidth)), Math.round(h))
    } else {
      const bx = Math.round(x - half)
      const bw = Math.max(1, Math.round(bodyWidth))
      const by = Math.round(top)
      const bh = Math.round(h)
      ctx.fillRect(bx, by, bw, bh)
      ctx.strokeRect(bx + 0.5, by + 0.5, bw - 1, Math.max(1, bh - 1))
    }
  }

  if (!showEma) return
  ctx.strokeStyle = theme.ema
  ctx.lineWidth = 1.4
  ctx.beginPath()
  let started = false
  for (let i = from; i <= to; i += 1) {
    const b = scale.bars[i]
    if (!b || b.ema20 === null) {
      started = false
      continue
    }
    const x = scale.xOf(i)
    const y = scale.yOf(b.ema20)
    if (!started) {
      ctx.moveTo(x, y)
      started = true
    } else {
      ctx.lineTo(x, y)
    }
  }
  ctx.stroke()
}

/**
 * Doji X + Blue ZigZag（Pine 脚本的移植，算式全在 chart/indicators.ts）。
 *
 * 画法是有意「比原脚本多一点」的：
 *   - 原脚本用 `plot()` 画蓝点，na 一断，孤立的那几个点在实际渲染里几乎看不见；
 *     这里画成**实心圆点 + 背景色描边**，扎在影线尖端上，一眼能数清有几个转折；
 *     点与点之间再连一条同色细线（原脚本没有），高低点连起来就是一条 ZigZag。
 *   - 原脚本的 `✖` 是 `color.black`，白底上直接用会糊进网格。这里取主题的
 *     `doji`（近黑），形状和位置保持「阳线在上、阴线在下」不变。
 *
 * 只画可见区间里的点：几百根 K 线的指标数组每帧都扫一遍是最容易白烧 CPU 的地方。
 * 折线例外 —— 左右各多取一个屏外的点，否则跨出屏幕的那一段会贴边断掉。
 */
function drawIndicator(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  indicator: IndicatorSettings,
): void {
  if (!indicator.show) return
  const series = indicatorSeries(scale.bars, indicator.lookback, indicator.dojiSize)
  if (series.doji.length === 0 && series.zigzag.length === 0) return

  const { plot } = scale.layout
  const { from, to } = scale.visibleRange()

  ctx.save()

  // 折线：把相邻的两个转折点连起来。
  //
  // 原脚本只有 `plot(cond ? low : na)`，na 一断就是散点 —— 这里按需求补一条连线。
  // 只连**相邻两个转折点**，不跨断开区间补点：每个转折点本身就是真实的高低点，
  // 连线只是把它们按时间顺序串起来，不改变任何一个点的价格与位置。
  //
  // 两侧各多画一个屏外的点：`from`/`to` 是可见区间，只画区间内的点的话，
  // 跨出屏幕的那一段会贴边断掉，看起来像线头。多画一个点再交给 clip 去裁，边缘就自然了。
  const pts = series.zigzag
  let lo = 0
  let hi = pts.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (pts[mid].index < from) lo = mid + 1
    else hi = mid
  }
  const startAt = Math.max(0, lo - 1)
  if (startAt < pts.length - 1) {
    ctx.save()
    ctx.beginPath()
    ctx.rect(plot.left, plot.top, plot.width, plot.height)
    ctx.clip()
    ctx.beginPath()
    ctx.lineWidth = 1.25
    ctx.lineJoin = 'round'
    ctx.lineCap = 'round'
    ctx.strokeStyle = theme.zigzagLine
    let started = false
    for (let i = startAt; i < pts.length; i += 1) {
      const p = pts[i]
      const x = scale.xOf(p.index)
      const y = scale.yOf(p.price)
      if (started) ctx.lineTo(x, y)
      else {
        ctx.moveTo(x, y)
        started = true
      }
      // 已经越过右边界还多画一个点，就够把出屏那一段连上了
      if (p.index > to) break
    }
    ctx.stroke()
    ctx.restore()
  }

  // ✖ 先画：小标记，别压在蓝点上面（同一个转折上两样都出现时，蓝点才是重点）
  ctx.font = '600 11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
  ctx.textAlign = 'center'
  ctx.fillStyle = theme.doji
  for (const d of series.doji) {
    if (d.index < from || d.index > to) continue
    const bar = scale.bars[d.index]
    if (!bar) continue
    // Pine 的 location.abovebar 是「顶在这根的最高价上方」，belowbar 是「吊在最低价下方」
    const y = d.above ? scale.yOf(bar.high) - 4 : scale.yOf(bar.low) + 4
    if (y < plot.top - 2 || y > plot.bottom + 2) continue
    ctx.textBaseline = d.above ? 'bottom' : 'top'
    ctx.fillText('✖', Math.round(scale.xOf(d.index)) + 0.5, y)
  }

  // 蓝点
  for (const p of series.zigzag) {
    if (p.index < from || p.index > to) continue
    const x = Math.round(scale.xOf(p.index)) + 0.5
    const y = scale.yOf(p.price)
    if (y < plot.top - 4 || y > plot.bottom + 4) continue
    ctx.beginPath()
    ctx.arc(x, y, 3.2, 0, Math.PI * 2)
    ctx.fillStyle = theme.zigzag
    ctx.fill()
    ctx.lineWidth = 1
    ctx.strokeStyle = theme.bg
    ctx.stroke()
  }

  ctx.restore()
}

/**
 * Bar Count：在这根 K 线的**最低价下方**写「当天第几根」。
 *
 * 编号本身由 chart/barcount.ts 算（每张 CSV 从 #1 起、只出单数），这里只管排版：
 *
 *   - 文字**居中对齐到 K 线**，基线是 top，所以它挂在最低价的下面而不是压住影线；
 *   - 和 Doji 的 ✖ 撞位置：十字星的 ✖ 也吊在最低价下方（`above === false`），
 *     同一根上两样都在时编号往下让一行 —— 11px 的字和 ✖ 叠在一起两边都读不出来；
 *   - **挨太近就跳过**：缩得很小时相邻编号会糊成一团。这里按「量出来的宽度」判重叠，
 *     宁可少标几个也要保证每个都在，而不是画一片看不清的墨点；
 *   - 整块 clip 在价格区里：贴边的编号被裁掉，不会画到时间轴上去。
 */
function drawBarCount(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  settings: BarCountSettings,
  indicator: IndicatorSettings,
): void {
  if (!settings.show) return
  const marks = barCountMarks(scale.bars)
  if (marks.length === 0) return

  const { plot } = scale.layout
  const { from, to } = scale.visibleRange()

  // 哪些 K 线的下方有 ✖（Doji 关掉时不必算）
  const dojiBelow = new Set<number>()
  if (indicator.show) {
    for (const d of indicatorSeries(scale.bars, indicator.lookback, indicator.dojiSize).doji) {
      if (!d.above) dojiBelow.add(d.index)
    }
  }

  ctx.save()
  ctx.beginPath()
  ctx.rect(plot.left, plot.top, plot.width, plot.height)
  ctx.clip()
  ctx.font = '11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  ctx.fillStyle = settings.color

  /** 上一个真正画出去的编号的右边缘：靠它挡掉重叠的邻居 */
  let lastRight = Number.NEGATIVE_INFINITY
  for (const m of marks) {
    if (m.index < from) continue
    if (m.index > to) break
    const bar = scale.bars[m.index]
    if (!bar) continue
    const y = scale.yOf(bar.low) + (dojiBelow.has(m.index) ? 18 : 4)
    // 这根的最低价已经在屏幕底边附近了：写下去只会被裁成半个字
    if (y + 11 > plot.bottom) continue
    const text = String(m.count)
    const w = ctx.measureText(text).width
    const x = Math.round(scale.xOf(m.index)) + 0.5
    if (x - w / 2 < lastRight + 2) continue
    ctx.fillText(text, x, y)
    lastRight = x + w / 2
  }

  ctx.restore()
}

/**
 * 一张挂单「如果现在成交」能平掉多少手、赚亏多少钱。
 *
 * 只管**对手单**（对着现有仓位方向的那些）：多头仓位下卖出才是减仓，卖多少手
 * 也只能减到 0 —— 手上 2 手多，挂一张卖 3 手，这 3 手里只有 2 手是平仓，
 * 剩下 1 手会反手开空，那 1 手的盈亏现在还没法说，所以**以仓位为准**按 2 手算。
 * 顺方向的挂单（多头下的买入）是加仓，加仓的盈亏取决于将来在哪儿平，
 * 这里不猜，也就不显示。
 *
 * 口径和后端 `closedLots` / `pointValueOf` 一致：(平 - 开) × 手数 × 每点价值，
 * 每点价值 = tickValue / tick。于是「画布上预告的数字」和「真成交后记录里的数字」
 * 是同一把尺子算出来的。
 */
function hedgePnl(order: Order, position: Position, pointValue: number): number | null {
  if (!position || position.side === 'flat' || !(position.averagePrice > 0)) return null
  if (!(pointValue > 0) || !(order.quantity > 0)) return null
  const closing = position.side === 'long' ? order.side === 'sell' : order.side === 'buy'
  if (!closing) return null
  const lots = Math.min(order.quantity, Math.abs(position.quantity))
  if (!(lots > 0)) return null
  const diff =
    position.side === 'long'
      ? order.price - position.averagePrice
      : position.averagePrice - order.price
  return diff * lots * pointValue
}

/** 美元金额：带千分位、最多两位小数、不给整数加 `.00`（+1,240$ / -86.5$），画布上够短才看得清 */
export function formatUsdShort(v: number): string {
  const body = Math.abs(v).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  })
  return `${v > 0 ? '+' : v < 0 ? '-' : ''}${body}$`
}

/**
 * 挂单：一条贯穿价格区的横线 + 右端一个标签（买/卖 + 单型 + 手数）。
 *
 * 右边的 `ORDER_RESERVE_RIGHT` 是给「撤单 ×」留的位置 —— 那个按钮是 HTML 叠在画布上的
 * （见 ChartPanel.vue），不占画布绘制，但标签必须让开，否则会顶到按钮底下。
 * 当前被拖动/悬停的那一张会加粗、并在标签里带上价格。
 *
 * 有仓位时，**对手单**的标签左边还会多一块「成交后大概赚亏多少」（见 `hedgePnl`）。
 */
function drawOrders(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  orders: Order[],
  position: Position,
  pointValue: number,
  activeId: string | null,
  decimals: number,
): void {
  const { plot } = scale.layout
  const open = orders.filter((o) => o.status === 'open')
  if (open.length === 0) return

  ctx.font = MONO
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  for (const o of open) {
    const active = o.id === activeId
    const y = Math.round(scale.yOf(o.price)) + 0.5
    if (y < plot.top || y > plot.bottom) continue
    const isBuy = o.side === 'buy'
    ctx.strokeStyle = isBuy ? theme.orderBuy : theme.orderSell
    ctx.lineWidth = active ? 2 : 1
    ctx.setLineDash(o.kind === 'stop' ? [5, 4] : [])
    ctx.beginPath()
    ctx.moveTo(plot.left, y)
    ctx.lineTo(plot.right, y)
    ctx.stroke()
    ctx.setLineDash([])

    // 市价单成交后立刻是 filled，正常情况下进不到这里；留着是为了画布上的文案和后端 kind 对齐
    const kindText = o.kind === 'limit' ? '限价' : o.kind === 'market' ? '市价' : '止损'
    const label = active
      ? `${isBuy ? '买' : '卖'}${kindText} ${o.quantity} @ ${formatPrice(o.price, decimals)}`
      : `${isBuy ? '买' : '卖'}${kindText} ${o.quantity}`
    ctx.font = active ? MONO_BOLD : MONO
    const textWidth = ctx.measureText(label).width
    const boxX = plot.right - ORDER_RESERVE_RIGHT - textWidth - 8
    ctx.fillStyle = isBuy ? theme.orderBuyBox : theme.orderSellBox
    ctx.fillRect(boxX - 4, y - 9, textWidth + 8, AXIS_LABEL_H)
    ctx.fillStyle = theme.orderText
    ctx.fillText(label, boxX, y + 0.5)

    /*
     * 有仓位时，对手单旁边再挂一块「成交后大概赚亏多少」。
     *
     * 贴在挂单标签**左边**、同一行，读起来是「卖限价 2 [-86.5$]」——
     * 一块小色块比在标签里塞一句彩色文字清楚：标签的底色是单方向色，
     * 里面再写绿/红数字会和底色互相打架。
     */
    const hedge = hedgePnl(o, position, pointValue)
    if (hedge === null) continue
    const money = formatUsdShort(hedge)
    ctx.font = MONO
    const mw = ctx.measureText(money).width + 8
    const mx = boxX - 8 - mw
    // 价格区左边已经写不下时就不画（宁可少一句，也不要盖住持仓线/绘图）
    if (mx < plot.left) continue
    ctx.fillStyle = hedge >= 0 ? theme.textUp : theme.textDown
    ctx.fillRect(mx, y - 9, mw, AXIS_LABEL_H)
    ctx.fillStyle = theme.orderText
    ctx.fillText(money, mx + 4, y + 0.5)
  }
  ctx.font = MONO
}

/**
 * 未平仓持仓：一条横线贯穿整个价格区，标出**成本价**和**浮动盈亏**。
 *
 * 标签写浮动盈亏而不是已实现盈亏：仓位没平时已实现盈亏一直是 0，写在成本线上
 * 等于什么都没说；浮动盈亏才是「这条线现在值多少钱」。
 *
 * 多头绿、空头红 —— 和白底上的文字色同一组数值，画布与列表不会出现两种绿。
 * 标签贴左边：右边留给挂单标签（也是同一行的矩形块），两者不会叠在一起。
 */
function drawPositionLine(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  position: Position,
  unrealizedPnl: number,
  decimals: number,
): void {
  if (!position || position.side === 'flat' || !(position.averagePrice > 0)) return
  const { plot } = scale.layout
  const y = Math.round(scale.yOf(position.averagePrice)) + 0.5
  if (y < plot.top || y > plot.bottom) return

  const long = position.side === 'long'
  const qty = long ? position.quantity : -position.quantity

  ctx.strokeStyle = long ? theme.posLong : theme.posShort
  ctx.lineWidth = 1.4
  ctx.beginPath()
  ctx.moveTo(plot.left, y)
  ctx.lineTo(plot.right, y)
  ctx.stroke()

  const label =
    `${long ? '多' : '空'} ${Math.abs(qty)} @ ${formatPrice(position.averagePrice, decimals)}` +
    ` · 浮动 ${formatSigned(unrealizedPnl, decimals)}`
  ctx.font = MONO_BOLD
  const w = ctx.measureText(label).width + 12
  ctx.fillStyle = long ? theme.posLongBox : theme.posShortBox
  ctx.fillRect(plot.left + 2, y - 9, w, 18)
  ctx.fillStyle = theme.posText
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  ctx.fillText(label, plot.left + 8, y + 0.5)
}

/**
 * 成交标记：每笔成交一个三角（买 = 上三角、卖 = 下三角），
 * 再加一条「开仓价 → 平仓价」的虚线 —— 一眼看出这一仓是赚是亏、拿了多久。
 *
 * 全部画在 Canvas 上（不是 SVG 标注），所以**不能选中、不能拖动、不能删除**。
 * `activeClosed` 是鼠标正指着（或已经钉住）的那一条：加粗一档，
 * 否则屏幕上几条虚线交织时，信息卡说的是哪一笔要靠猜。
 */
function drawTradeMarks(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  trades: Trade[],
  closed: ClosedTrade[],
  activeClosed: number | null,
): void {
  if (trades.length === 0) return
  const { plot } = scale.layout

  // 先画连线，三角形压在上面
  closed.forEach((c, i) => {
    const i1 = scale.indexOfTime(c.openTime)
    const i2 = scale.indexOfTime(c.closeTime)
    if (i1 === undefined || i2 === undefined) return
    const active = i === activeClosed
    ctx.strokeStyle = c.side === 'long' ? theme.tradeBuy : theme.tradeSell
    ctx.globalAlpha = active ? 1 : 0.7
    ctx.lineWidth = active ? 2.4 : 1.2
    ctx.setLineDash([4, 3])
    ctx.beginPath()
    ctx.moveTo(scale.xOf(i1), scale.yOf(c.openPrice))
    ctx.lineTo(scale.xOf(i2), scale.yOf(c.closePrice))
    ctx.stroke()
    ctx.setLineDash([])
    ctx.globalAlpha = 1
  })

  const half = 5
  const tall = 7
  for (const t of trades) {
    const i = scale.indexOfTime(t.time)
    if (i === undefined) continue
    const x = scale.xOf(i)
    const y = scale.yOf(t.price)
    if (y < plot.top - tall || y > plot.bottom + tall) continue

    ctx.beginPath()
    if (t.side === 'buy') {
      // 上三角：尖朝上顶住成交价，身子在价格下方
      ctx.moveTo(x, y - 1)
      ctx.lineTo(x - half, y + tall)
      ctx.lineTo(x + half, y + tall)
    } else {
      // 下三角：尖朝下顶住成交价，身子在价格上方
      ctx.moveTo(x, y + 1)
      ctx.lineTo(x - half, y - tall)
      ctx.lineTo(x + half, y - tall)
    }
    ctx.closePath()
    ctx.fillStyle = t.side === 'buy' ? theme.tradeBuy : theme.tradeSell
    ctx.fill()
    ctx.strokeStyle = theme.bg
    ctx.lineWidth = 1
    ctx.stroke()
  }
}

function drawLastPrice(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  decimals: number,
): void {
  const last = scale.bars[scale.bars.length - 1]
  if (!last) return
  const y = Math.round(scale.yOf(last.close)) + 0.5
  const { plot } = scale.layout
  if (y < plot.top || y > plot.bottom) return

  // 现价虚线和右侧标签都走 K 线那套黑白：K 线本身不带涨跌色，
  // 这里再染一层绿红只会让「现价线」比 K 线还抢眼。
  ctx.strokeStyle = theme.up.stroke
  ctx.globalAlpha = 0.55
  ctx.setLineDash([3, 3])
  ctx.beginPath()
  ctx.moveTo(plot.left, y)
  ctx.lineTo(plot.right, y)
  ctx.stroke()
  ctx.setLineDash([])
  ctx.globalAlpha = 1

  drawAxisBox(ctx, scale, formatPrice(last.close, decimals), y, theme.lastBoxBg, theme.lastBoxText)
}

function drawAxisBox(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  text: string,
  y: number,
  bg: string,
  fg: string,
): void {
  const { plot } = scale.layout
  ctx.font = MONO_BOLD
  const h = AXIS_LABEL_H
  ctx.fillStyle = bg
  ctx.fillRect(plot.right + 1, y - h / 2, scale.width - plot.right - 2, h)
  ctx.fillStyle = fg
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  ctx.fillText(text, plot.right + 5, y + 0.5)
}

function drawAxes(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  ticks: number[],
  decimals: number,
  tick: number,
): void {
  const { plot, timeAxis } = scale.layout
  ctx.font = MONO
  ctx.fillStyle = theme.textDim
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  for (const v of ticks) {
    const y = scale.yOf(v)
    if (y < plot.top - 2 || y > plot.bottom + 2) continue
    ctx.fillText(formatPrice(v, decimals), plot.right + 5, y)
  }

  // 最小跳动：写在价格轴最下面（时间轴那一行的右侧空位），不占行情区
  if (tick > 0) {
    ctx.textAlign = 'left'
    ctx.textBaseline = 'bottom'
    ctx.fillStyle = theme.textDim
    ctx.fillText(`tick ${formatTick(tick)}`, plot.right + 5, scale.height - 3)
  }

  // 时间轴：**完整日期时间**「周五 2026-10-16 09:35」。
  // 只写 09:35 看不出是哪一天，复盘跨了好几天的行情时特别容易看错。
  const step = scale.timeTickStep()
  const { from, to } = scale.visibleRange()
  const first = Math.ceil(from / step) * step
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  for (let i = first; i <= to; i += step) {
    const b = scale.bars[i]
    if (!b) continue
    const x = scale.xOf(i)
    const label = formatAxisTime(b.time)
    const half = ctx.measureText(label).width / 2 + 4
    // 贴边的那两个标签会各被裁掉一半，干脆不画
    if (x - half < plot.left || x + half > plot.right) continue
    ctx.fillStyle = theme.textDim
    ctx.fillText(label, x, timeAxis.top + 5)
  }
}

function drawWatermark(
  ctx: CanvasRenderingContext2D,
  layout: ChartLayout,
  theme: ChartTheme,
  symbol: string,
): void {
  if (!symbol) return
  ctx.save()
  ctx.globalAlpha = 0.07
  ctx.fillStyle = theme.text
  ctx.font = '700 28px system-ui, "Microsoft YaHei", sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText(symbol, layout.plot.left + layout.plot.width / 2, layout.plot.top + layout.plot.height / 2)
  ctx.restore()
}

function drawCrosshair(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  hover: HoverState,
  decimals: number,
  tick: number,
): void {
  const { plot, timeAxis } = scale.layout
  const b = scale.bars[hover.index]
  if (!b) return

  // 指针在 ATR 副图里：那儿**没有价格可言**（副图的纵轴是波幅，不是报价），
  // 所以只留竖线、时间标签和顶部的 OHLC 图例 —— 横线、吸附圈、价格标签全部不画，
  // 否则右边价格轴上会冒出一个按副图像素外推出来的、屏幕上根本不存在的价。
  const below = !!(scale.layout.atr && hover.y > scale.layout.atr.top)

  // 按住 Ctrl 时十字线**自己跳到 OHLC 上**，横竖两条线一起跳。
  //
  // 这一条以前没有：那时吸附只在「画下去的那一刻」发生，用户按住 Ctrl 之后十字线纹丝不动，
  // 得先点一下才知道起点落在哪儿 —— 点歪了只能 Esc 重来。现在按住 Ctrl 就能看见落点。
  //
  // 吸附在这里**每帧现算**（不读缓存值）：缩放/平移/步进之后它永远是对的。
  // 没吸时照旧：价格吸到最小跳动（MES 只有 .00/.25/.50/.75，不该出现 6484.12）。
  const hit = hover.snap && !below ? snapAtCursor(scale, hover.x, hover.y) : null
  const bar = hit ? (scale.bars[hit.index] ?? b) : b
  const price = hit ? hit.price : snapToTick(scale.priceAtY(hover.y), tick)
  const x = Math.round(scale.xOf(hit ? hit.index : hover.index)) + 0.5
  const y = clamp(scale.yOf(price), plot.top, plot.bottom)

  ctx.strokeStyle = hit ? theme.snap : theme.crosshair
  ctx.lineWidth = hit ? 1.4 : 1
  ctx.setLineDash([4, 4])
  ctx.beginPath()
  // 竖线一直画到底（开着 ATR 时穿过副图）：横轴是共用的，光标落在哪一根，
  // 副图上那个时刻的 ATR 就该在同一列上被读到。
  ctx.moveTo(x, plot.top)
  ctx.lineTo(x, scale.layout.atr ? scale.layout.atr.bottom : plot.bottom)
  if (!below) {
    ctx.moveTo(plot.left, Math.round(y) + 0.5)
    ctx.lineTo(plot.right, Math.round(y) + 0.5)
  }
  ctx.stroke()
  ctx.setLineDash([])

  // 吸住的那一点上画个小圈：告诉用户「价格和这根 K 线绑上了」，而不是随手停在半路
  if (hit) {
    ctx.beginPath()
    ctx.arc(x, y, 4, 0, Math.PI * 2)
    ctx.strokeStyle = theme.snap
    ctx.lineWidth = 1.6
    ctx.stroke()
  }

  // 价格标签：吸附生效时底色调成吸附色（在副图里时不画，见上面的 below）
  if (!below) {
    drawAxisBox(
      ctx,
      scale,
      formatPrice(price, decimals),
      Math.round(y),
      hit ? theme.snap : theme.crossBoxBg,
      hit ? theme.snapText : theme.crossBoxText,
    )
  }

  // 时间标签：跟着**被吸住的那根 K 线**走（竖线在它身上，标签就必须是它的时间）
  ctx.font = MONO
  const label = formatAxisTime(bar.time)
  const w = ctx.measureText(label).width + 10
  const boxX = Math.min(Math.max(x - w / 2, plot.left), scale.width - w - 2)
  ctx.fillStyle = hit ? theme.snap : theme.crossBoxBg
  ctx.fillRect(boxX, timeAxis.top + 2, w, AXIS_LABEL_H)
  ctx.fillStyle = theme.crossBoxText
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  ctx.fillText(label, boxX + w / 2, timeAxis.top + 4)

  // OHLC 图例（顶部一行）：吸住的那一项换成吸附色 —— 用户一眼看出吸到的是哪个价。
  // 四个价格同色：K 线是黑白的，图例再分涨跌色反而和画布对不上。
  const upColor = theme.text
  const parts: Array<[string, string, string, OHLCItem]> = [
    ['开', formatPrice(bar.open, decimals), upColor, 'open'],
    ['高', formatPrice(bar.high, decimals), theme.text, 'high'],
    ['低', formatPrice(bar.low, decimals), theme.text, 'low'],
    ['收', formatPrice(bar.close, decimals), upColor, 'close'],
  ]
  let lx = plot.left + 6
  ctx.textAlign = 'left'
  ctx.textBaseline = 'top'
  ctx.font = MONO
  for (const [name, value, color, item] of parts) {
    const on = hit?.item === item
    ctx.fillStyle = on ? theme.snap : theme.textDim
    ctx.fillText(name, lx, plot.top + 3)
    lx += ctx.measureText(name).width + 4
    ctx.fillStyle = on ? theme.snap : color
    ctx.fillText(value, lx, plot.top + 3)
    lx += ctx.measureText(value).width + 12
  }
  if (bar.ema20 !== null) {
    ctx.fillStyle = theme.textDim
    ctx.fillText('EMA20', lx, plot.top + 3)
    lx += ctx.measureText('EMA20').width + 4
    ctx.fillStyle = theme.ema
    ctx.fillText(formatPrice(bar.ema20, decimals), lx, plot.top + 3)
    lx += ctx.measureText(formatPrice(bar.ema20, decimals)).width + 12
  }

  // 这一根的 ATR：参数就是【指标 ▾】里那套 Length / Smoothing（`scale` 里的序列
  // 由 ChartPanel 用同一组参数灌进来），所以栏里的数和副图那条线永远是同一个值。
  // 副图关掉也照常显示 —— 关的只是画不画折线，不是「这条指标不存在」。
  const atrValue = scale.atrAt(hit ? hit.index : hover.index)
  ctx.fillStyle = theme.textDim
  ctx.fillText('ATR', lx, plot.top + 3)
  lx += ctx.measureText('ATR').width + 4
  ctx.fillStyle = theme.atr
  const atrText = atrValue === null ? '—' : formatPrice(atrValue, decimals)
  ctx.fillText(atrText, lx, plot.top + 3)
  lx += ctx.measureText(atrText).width + 12

  // 涨跌 = 这一根收盘 - 前一根收盘（价差，不是涨跌幅）。正绿负红，和页面同一套语义色。
  // 第一根没有前收，写「—」而不是 0：0 会被读成「平」，那是另一件事。
  const prev = scale.bars[(hit ? hit.index : hover.index) - 1]
  ctx.fillStyle = theme.textDim
  ctx.fillText('涨跌', lx, plot.top + 3)
  lx += ctx.measureText('涨跌').width + 4
  if (!prev) {
    ctx.fillStyle = theme.textDim
    ctx.fillText('—', lx, plot.top + 3)
  } else {
    const diff = bar.close - prev.close
    ctx.fillStyle = diff > 0 ? theme.textUp : diff < 0 ? theme.textDown : theme.textDim
    ctx.fillText(formatSigned(diff, decimals), lx, plot.top + 3)
  }
}
