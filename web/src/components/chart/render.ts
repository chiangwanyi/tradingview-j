import type { Order } from '@/types/protocol'

import {
  ChartScale,
  decimalsForStep,
  formatPrice,
  niceTicks,
  type ChartLayout,
} from './scale'

/**
 * Canvas 上画的东西：网格、坐标轴、K 线、EMA20、BarCount、挂单价线、十字光标。
 *
 * 标注不在这里画 —— 标注是 SVG 图层（便于命中测试与拖动），见 ChartPanel.vue。
 * 两层的坐标换算都来自同一个 ChartScale，不会出现「图形和 K 线对不上」。
 */

export interface ChartTheme {
  bg: string
  grid: string
  axis: string
  text: string
  textDim: string
  up: string
  down: string
  flat: string
  ema: string
  crosshair: string
  panelBg: string
  panelBorder: string
}

/** 深色看盘配色。涨红跌绿是中国市场的约定，与欧美相反，这里按中国口径。 */
export const DARK_THEME: ChartTheme = {
  bg: '#0b1120',
  grid: '#1e293b',
  axis: '#1f2a3c',
  text: '#cbd5e1',
  textDim: '#7c8ba1',
  up: '#ef4444',
  down: '#22c55e',
  flat: '#94a3b8',
  ema: '#fbbf24',
  crosshair: '#64748b',
  panelBg: '#0e1626',
  panelBorder: '#1e293b',
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
  showEma: boolean
}

const MONO = '11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
const MONO_BOLD = '600 11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

export function drawChart(input: DrawInput): void {
  const { ctx, scale, theme, hover, symbol, orders, showEma } = input
  const { width, height } = scale
  const layout: ChartLayout = scale.layout

  ctx.save()
  ctx.clearRect(0, 0, width, height)
  ctx.fillStyle = theme.bg
  ctx.fillRect(0, 0, width, height)
  if (scale.bars.length === 0) {
    ctx.fillStyle = theme.textDim
    ctx.font = MONO
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText('还没有数据：先在左侧选一个标的、建一个实例', width / 2, height / 2)
    ctx.restore()
    return
  }

  drawPanels(ctx, layout, theme)
  const priceTicks = niceTicks(scale.priceMin, scale.priceMax, 6)
  const priceStep =
    priceTicks.length > 1 ? Math.abs(priceTicks[1] - priceTicks[0]) : (scale.priceMax - scale.priceMin) / 6
  const decimals = decimalsForStep(priceStep)

  drawGrid(ctx, scale, theme, priceTicks)
  drawCandles(ctx, scale, theme, showEma)
  drawBarCount(ctx, scale, theme)
  drawOrders(ctx, scale, orders)
  drawLastPrice(ctx, scale, theme, decimals)
  drawAxes(ctx, scale, theme, priceTicks, decimals)
  drawWatermark(ctx, layout, theme, symbol)
  if (hover && hover.inside) drawCrosshair(ctx, scale, theme, hover, decimals)

  ctx.restore()
}

function drawPanels(ctx: CanvasRenderingContext2D, layout: ChartLayout, theme: ChartTheme): void {
  ctx.fillStyle = theme.panelBg
  ctx.fillRect(layout.panel.left, layout.panel.top, layout.panel.width, layout.panel.height)
  ctx.strokeStyle = theme.panelBorder
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(layout.panel.left, layout.panel.top + 0.5)
  ctx.lineTo(layout.panel.right, layout.panel.top + 0.5)
  ctx.stroke()
}

function drawGrid(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  theme: ChartTheme,
  ticks: number[],
): void {
  const { plot, panel } = scale.layout
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
    ctx.lineTo(x, panel.bottom)
  }
  ctx.stroke()
  // 指标区底线
  ctx.strokeStyle = theme.panelBorder
  const baseY = Math.round(panel.bottom) + 0.5
  ctx.beginPath()
  ctx.moveTo(panel.left, baseY)
  ctx.lineTo(panel.right, baseY)
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
    const color = up ? theme.up : down ? theme.down : theme.flat

    ctx.strokeStyle = color
    ctx.fillStyle = color
    ctx.lineWidth = 1

    const yHigh = scale.yOf(b.high)
    const yLow = scale.yOf(b.low)
    ctx.beginPath()
    ctx.moveTo(Math.round(x) + 0.5, yHigh)
    ctx.lineTo(Math.round(x) + 0.5, yLow)
    ctx.stroke()

    const yOpen = scale.yOf(b.open)
    const yClose = scale.yOf(b.close)
    const top = Math.min(yOpen, yClose)
    const h = Math.max(1, Math.abs(yClose - yOpen))
    if (bodyWidth <= 1.6) {
      ctx.fillRect(Math.round(x), top, 1, h)
    } else {
      ctx.fillRect(Math.round(x - half), Math.round(top), Math.round(bodyWidth), Math.round(h))
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

function drawBarCount(ctx: CanvasRenderingContext2D, scale: ChartScale, theme: ChartTheme): void {
  const { panel } = scale.layout
  const { from, to } = scale.visibleRange()
  const max = scale.panelMax()

  ctx.strokeStyle = 'rgba(56, 189, 248, 0.75)'
  ctx.lineWidth = 1.2
  ctx.beginPath()
  let started = false
  let prevY = 0
  for (let i = from; i <= to; i += 1) {
    const b = scale.bars[i]
    if (!b) continue
    const x = scale.xOf(i)
    const y = scale.yOfBarCount(b.barCount, max)
    if (!started) {
      ctx.moveTo(x, y)
      started = true
    } else {
      // 阶梯线：先横再竖，才能看出「每根加一」
      ctx.lineTo(x, prevY)
      ctx.lineTo(x, y)
    }
    prevY = y
  }
  ctx.stroke()

  ctx.fillStyle = theme.textDim
  ctx.font = MONO
  ctx.textAlign = 'left'
  ctx.textBaseline = 'top'
  ctx.fillText('BarCount', panel.left + 4, panel.top + 4)
  const last = scale.bars[scale.bars.length - 1]
  if (last) {
    ctx.textAlign = 'right'
    ctx.fillText(String(last.barCount), panel.right - 2, panel.top + 4)
  }
}

function drawOrders(ctx: CanvasRenderingContext2D, scale: ChartScale, orders: Order[]): void {
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
    ctx.strokeStyle = isBuy ? 'rgba(96, 165, 250, 0.8)' : 'rgba(251, 146, 60, 0.8)'
    ctx.lineWidth = 1
    ctx.setLineDash(o.kind === 'stop' ? [5, 4] : [])
    ctx.beginPath()
    ctx.moveTo(plot.left, y)
    ctx.lineTo(plot.right, y)
    ctx.stroke()
    ctx.setLineDash([])

    const label = `${isBuy ? '买' : '卖'}${o.kind === 'limit' ? '限价' : '止损'} ${o.quantity}`
    const textWidth = ctx.measureText(label).width
    const boxX = plot.right - textWidth - 8
    ctx.fillStyle = isBuy ? 'rgba(30, 64, 175, 0.85)' : 'rgba(124, 45, 18, 0.85)'
    ctx.fillRect(boxX - 3, y - 8, textWidth + 6, 15)
    ctx.fillStyle = '#e2e8f0'
    ctx.fillText(label, boxX, y + 0.5)
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

  const color = last.close >= last.open ? theme.up : theme.down
  ctx.strokeStyle = color
  ctx.globalAlpha = 0.55
  ctx.setLineDash([3, 3])
  ctx.beginPath()
  ctx.moveTo(plot.left, y)
  ctx.lineTo(plot.right, y)
  ctx.stroke()
  ctx.setLineDash([])
  ctx.globalAlpha = 1

  drawAxisBox(ctx, scale, formatPrice(last.close, decimals), y, color)
}

function drawAxisBox(
  ctx: CanvasRenderingContext2D,
  scale: ChartScale,
  text: string,
  y: number,
  bg: string,
): void {
  const { plot } = scale.layout
  ctx.font = MONO_BOLD
  const h = 16
  ctx.fillStyle = bg
  ctx.fillRect(plot.right + 1, y - h / 2, scale.width - plot.right - 2, h)
  ctx.fillStyle = '#0b1120'
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
): void {
  const { plot, panel } = scale.layout
  ctx.font = MONO
  ctx.fillStyle = theme.textDim
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  for (const v of ticks) {
    const y = scale.yOf(v)
    if (y < plot.top - 2 || y > plot.bottom + 2) continue
    ctx.fillText(formatPrice(v, decimals), plot.right + 5, y)
  }

  // 时间轴
  const step = scale.timeTickStep()
  const { from, to } = scale.visibleRange()
  const first = Math.ceil(from / step) * step
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  let prevDate = ''
  for (let i = first; i <= to; i += step) {
    const b = scale.bars[i]
    if (!b) continue
    const x = scale.xOf(i)
    if (x < plot.left + 20 || x > plot.right - 20) continue
    const date = b.time.slice(0, 10)
    const label = date !== prevDate ? b.time.slice(5, 16) : b.time.slice(11, 16)
    prevDate = date
    ctx.fillStyle = theme.textDim
    ctx.fillText(label, x, panel.bottom + 5)
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
  ctx.globalAlpha = 0.06
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
): void {
  const { plot, panel } = scale.layout
  const b = scale.bars[hover.index]
  if (!b) return
  const x = Math.round(scale.xOf(hover.index)) + 0.5

  ctx.strokeStyle = theme.crosshair
  ctx.lineWidth = 1
  ctx.setLineDash([4, 4])
  ctx.beginPath()
  ctx.moveTo(x, plot.top)
  ctx.lineTo(x, panel.bottom)
  ctx.moveTo(plot.left, Math.round(hover.y) + 0.5)
  ctx.lineTo(plot.right, Math.round(hover.y) + 0.5)
  ctx.stroke()
  ctx.setLineDash([])

  // 价格标签
  if (hover.y >= plot.top && hover.y <= plot.bottom) {
    drawAxisBox(ctx, scale, formatPrice(scale.priceAtY(hover.y), decimals), Math.round(hover.y), '#334155')
  }

  // 时间标签
  ctx.font = MONO
  const label = b.time.slice(5, 16)
  const w = ctx.measureText(label).width + 10
  const boxX = Math.min(Math.max(x - w / 2, plot.left), scale.width - w - 2)
  ctx.fillStyle = '#334155'
  ctx.fillRect(boxX, panel.bottom + 2, w, 16)
  ctx.fillStyle = '#e2e8f0'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  ctx.fillText(label, boxX + w / 2, panel.bottom + 4)

  // OHLC 图例
  const parts: Array<[string, string, string]> = [
    ['开', formatPrice(b.open, decimals), b.close >= b.open ? theme.up : theme.down],
    ['高', formatPrice(b.high, decimals), theme.text],
    ['低', formatPrice(b.low, decimals), theme.text],
    ['收', formatPrice(b.close, decimals), b.close >= b.open ? theme.up : theme.down],
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
