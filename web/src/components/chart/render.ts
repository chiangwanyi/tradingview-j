import type { Order, Position, Trade } from '@/types/protocol'

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
 * Canvas 上画的东西：网格、坐标轴、K 线、EMA20、挂单价线、十字光标。
 *
 * 标注不在这里画 —— 标注是 SVG 图层（便于命中测试与拖动），见 ChartPanel.vue。
 * 两层的坐标换算都来自同一个 ChartScale，不会出现「图形和 K 线对不上」。
 *
 * 两套配色（深色 / 浅色）都在这个文件里，组件只传一个 `theme` 进来：
 *
 *   深色：阳线绿、阴线红（实心）
 *   浅色：阳线**白填充 + 黑边框/影线**、阴线**全黑**（空心 vs 实心的经典画法，
 *         打印出来也分得清；纯红绿在白底上偏刺眼，且红绿色弱看着一样）
 */

/** 一根 K 线的描边与填充分开给：浅色模式靠「空心/实心」区分涨跌。 */
export interface CandleColors {
  stroke: string
  fill: string
}

export interface ChartTheme {
  mode: 'light' | 'dark'
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
  /** 成交标记：买入三角 / 卖出三角 / 「开仓→平仓」连线 */
  tradeBuy: string
  tradeSell: string
  /** 选中标注的虚线框 */
  selection: string
}

export const DARK_THEME: ChartTheme = {
  mode: 'dark',
  bg: '#0b1120',
  grid: '#1b2637',
  axisLine: '#1f2a3c',
  text: '#cbd5e1',
  textDim: '#7c8ba1',
  crosshair: '#64748b',
  up: { stroke: '#22c55e', fill: '#22c55e' },
  down: { stroke: '#ef4444', fill: '#ef4444' },
  flat: { stroke: '#94a3b8', fill: '#94a3b8' },
  ema: '#fbbf24',
  lastBoxBg: '#22c55e',
  lastBoxText: '#04130a',
  crossBoxBg: '#334155',
  crossBoxText: '#e2e8f0',
  orderBuy: 'rgba(96, 165, 250, 0.85)',
  orderSell: 'rgba(251, 146, 60, 0.85)',
  orderBuyBox: 'rgba(30, 64, 175, 0.9)',
  orderSellBox: 'rgba(124, 45, 18, 0.9)',
  orderText: '#e2e8f0',
  posLong: 'rgba(34, 197, 94, 0.9)',
  posShort: 'rgba(239, 68, 68, 0.9)',
  posLongBox: 'rgba(6, 78, 59, 0.92)',
  posShortBox: 'rgba(127, 29, 29, 0.92)',
  posText: '#f1f5f9',
  tradeBuy: '#4ade80',
  tradeSell: '#f87171',
  selection: '#cbd5e1',
}

export const LIGHT_THEME: ChartTheme = {
  mode: 'light',
  bg: '#ffffff',
  grid: '#e8eef6',
  axisLine: '#cbd5e1',
  text: '#0f172a',
  textDim: '#475569',
  crosshair: '#64748b',
  up: { stroke: '#111827', fill: '#ffffff' },
  down: { stroke: '#111827', fill: '#111827' },
  flat: { stroke: '#6b7280', fill: '#ffffff' },
  ema: '#b45309',
  lastBoxBg: '#111827',
  lastBoxText: '#ffffff',
  crossBoxBg: '#475569',
  crossBoxText: '#ffffff',
  orderBuy: 'rgba(37, 99, 235, 0.85)',
  orderSell: 'rgba(217, 119, 6, 0.85)',
  orderBuyBox: 'rgba(37, 99, 235, 0.95)',
  orderSellBox: 'rgba(180, 83, 9, 0.95)',
  orderText: '#ffffff',
  posLong: 'rgba(21, 128, 61, 0.9)',
  posShort: 'rgba(185, 28, 28, 0.9)',
  posLongBox: '#15803d',
  posShortBox: '#b91c1c',
  posText: '#ffffff',
  tradeBuy: '#15803d',
  tradeSell: '#b91c1c',
  selection: '#334155',
}

export function themeOf(mode: 'light' | 'dark'): ChartTheme {
  return mode === 'light' ? LIGHT_THEME : DARK_THEME
}

export interface HoverState {
  index: number
  x: number
  y: number
  inside: boolean
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
  showEma: boolean
  /** 最小跳动，来自 _symbols.json（0.25 / 0.1 / 0.01…） */
  tick: number
}

const MONO = '12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
const MONO_BOLD = '600 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
/** 价格轴/时间轴上那种实心小标签的高度（12px 字放进去不挤） */
const AXIS_LABEL_H = 18

export function drawChart(input: DrawInput): void {
  const { ctx, scale, theme, hover, symbol, orders, trades, closed, position, unrealizedPnl, showEma, tick } =
    input
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
  drawCandles(ctx, scale, theme, showEma)
  // 成交标记压在 K 线上面：复盘时要一眼看到「哪里开、哪里平」
  drawTradeMarks(ctx, scale, theme, trades, closed)
  drawOrders(ctx, scale, theme, orders)
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
  // 时间网格线（只画竖线，和标签同一个步长）
  const tickStep = scale.timeTickStep()
  const { from, to } = scale.visibleRange()
  const first = Math.ceil(from / tickStep) * tickStep
  for (let i = first; i <= to; i += tickStep) {
    const x = Math.round(scale.xOf(i)) + 0.5
    if (x < plot.left || x > plot.right) continue
    ctx.moveTo(x, plot.top)
    ctx.lineTo(x, plot.bottom)
  }
  ctx.stroke()

  // 价格区与时间轴之间的分隔线
  ctx.strokeStyle = theme.axisLine
  const baseY = Math.round(timeAxis.top) + 0.5
  ctx.beginPath()
  ctx.moveTo(plot.left, baseY)
  ctx.lineTo(plot.right, baseY)
  ctx.stroke()
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

    // 实体：先填后描边。浅色模式的「白填充 + 黑边框」就是靠这两步得到的；
    // 深色模式填充色和描边色相同，看起来是实心块。
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

function drawOrders(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  orders: Order[],
): void {
  const { plot } = scale.layout
  const open = orders.filter((o) => o.status === 'open')
  if (open.length === 0) return

  ctx.font = MONO
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  for (const o of open) {
    const y = Math.round(scale.yOf(o.price)) + 0.5
    if (y < plot.top || y > plot.bottom) continue
    const isBuy = o.side === 'buy'
    ctx.strokeStyle = isBuy ? theme.orderBuy : theme.orderSell
    ctx.lineWidth = 1
    ctx.setLineDash(o.kind === 'stop' ? [5, 4] : [])
    ctx.beginPath()
    ctx.moveTo(plot.left, y)
    ctx.lineTo(plot.right, y)
    ctx.stroke()
    ctx.setLineDash([])

    // 市价单成交后立刻是 filled，正常情况下进不到这里；留着是为了画布上的文案和后端 kind 对齐
    const kindText = o.kind === 'limit' ? '限价' : o.kind === 'market' ? '市价' : '止损'
    const label = `${isBuy ? '买' : '卖'}${kindText} ${o.quantity}`
    const textWidth = ctx.measureText(label).width
    const boxX = plot.right - textWidth - 8
    ctx.fillStyle = isBuy ? theme.orderBuyBox : theme.orderSellBox
    ctx.fillRect(boxX - 4, y - 9, textWidth + 8, AXIS_LABEL_H)
    ctx.fillStyle = theme.orderText
    ctx.fillText(label, boxX, y + 0.5)
  }
}

/**
 * 未平仓持仓：一条横线贯穿整个价格区，标出**成本价**和**浮动盈亏**。
 *
 * 标签写浮动盈亏而不是已实现盈亏：仓位没平时已实现盈亏一直是 0，写在成本线上
 * 等于什么都没说；浮动盈亏才是「这条线现在值多少钱」。
 *
 * 多头绿、空头红（深色主题是亮绿/亮红，浅色主题压暗一档，白底上才看得清）。
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
 * 再加一条「开仓均价 → 平仓价」的连线 —— 一眼看出这一仓是赚是亏、拿了多久。
 *
 * 全部画在 Canvas 上（不是 SVG 标注），所以**不能选中、不能拖动、不能删除**。
 */
function drawTradeMarks(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  trades: Trade[],
  closed: ClosedTrade[],
): void {
  if (trades.length === 0) return
  const { plot } = scale.layout

  // 先画连线，三角形压在上面
  for (const c of closed) {
    const i1 = scale.indexOfTime(c.openTime)
    const i2 = scale.indexOfTime(c.closeTime)
    if (i1 === undefined || i2 === undefined) continue
    ctx.strokeStyle = c.side === 'long' ? theme.tradeBuy : theme.tradeSell
    ctx.globalAlpha = 0.7
    ctx.lineWidth = 1.2
    ctx.setLineDash([4, 3])
    ctx.beginPath()
    ctx.moveTo(scale.xOf(i1), scale.yOf(c.openPrice))
    ctx.lineTo(scale.xOf(i2), scale.yOf(c.closePrice))
    ctx.stroke()
    ctx.setLineDash([])
    ctx.globalAlpha = 1
  }

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

  const up = last.close >= last.open
  const color = up ? theme.up.stroke : theme.down.stroke
  ctx.strokeStyle = color
  ctx.globalAlpha = 0.55
  ctx.setLineDash([3, 3])
  ctx.beginPath()
  ctx.moveTo(plot.left, y)
  ctx.lineTo(plot.right, y)
  ctx.stroke()
  ctx.setLineDash([])
  ctx.globalAlpha = 1

  // 标签底色跟着涨跌色走，但文字色单独给：浅色模式是「黑底白字」，
  // 深色模式是「绿底深字」，两者都保证对比度。
  drawAxisBox(ctx, scale, formatPrice(last.close, decimals), y, up ? theme.lastBoxBg : color, theme.lastBoxText)
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
  ctx.globalAlpha = theme.mode === 'light' ? 0.07 : 0.06
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
  const x = Math.round(scale.xOf(hover.index)) + 0.5

  // 光标的价格**吸附到最小跳动**：MES 只能停在 xxxx.00/.25/.50/.75，
  // 不会出现 6484.12 这种这个标的根本不存在的报价。横线也跟着这条价格走，
  // 所以看到标签上的数字，就是横线所在位置的真实价格。
  const price = snapToTick(scale.priceAtY(hover.y), tick)
  const y = clamp(scale.yOf(price), plot.top, plot.bottom)

  ctx.strokeStyle = theme.crosshair
  ctx.lineWidth = 1
  ctx.setLineDash([4, 4])
  ctx.beginPath()
  ctx.moveTo(x, plot.top)
  ctx.lineTo(x, plot.bottom)
  ctx.moveTo(plot.left, Math.round(y) + 0.5)
  ctx.lineTo(plot.right, Math.round(y) + 0.5)
  ctx.stroke()
  ctx.setLineDash([])

  // 价格标签
  drawAxisBox(ctx, scale, formatPrice(price, decimals), Math.round(y), theme.crossBoxBg, theme.crossBoxText)

  // 时间标签（同样给完整日期时间，和刻度标签一个格式）
  ctx.font = MONO
  const label = formatAxisTime(b.time)
  const w = ctx.measureText(label).width + 10
  const boxX = Math.min(Math.max(x - w / 2, plot.left), scale.width - w - 2)
  ctx.fillStyle = theme.crossBoxBg
  ctx.fillRect(boxX, timeAxis.top + 2, w, AXIS_LABEL_H)
  ctx.fillStyle = theme.crossBoxText
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  ctx.fillText(label, boxX + w / 2, timeAxis.top + 4)

  // OHLC 图例（顶部一行）
  const upColor = b.close >= b.open ? theme.up.stroke : theme.down.stroke
  const parts: Array<[string, string, string]> = [
    ['开', formatPrice(b.open, decimals), upColor],
    ['高', formatPrice(b.high, decimals), theme.text],
    ['低', formatPrice(b.low, decimals), theme.text],
    ['收', formatPrice(b.close, decimals), upColor],
  ]
  let lx = plot.left + 6
  ctx.textAlign = 'left'
  ctx.textBaseline = 'top'
  ctx.font = MONO
  for (const [name, value, color] of parts) {
    ctx.fillStyle = theme.textDim
    ctx.fillText(name, lx, plot.top + 3)
    lx += ctx.measureText(name).width + 4
    ctx.fillStyle = color
    ctx.fillText(value, lx, plot.top + 3)
    lx += ctx.measureText(value).width + 12
  }
  if (b.ema20 !== null) {
    ctx.fillStyle = theme.textDim
    ctx.fillText('EMA20', lx, plot.top + 3)
    lx += ctx.measureText('EMA20').width + 4
    ctx.fillStyle = theme.ema
    ctx.fillText(formatPrice(b.ema20, decimals), lx, plot.top + 3)
  }
}
