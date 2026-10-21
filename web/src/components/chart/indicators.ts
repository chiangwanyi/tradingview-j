import type { Bar } from '@/types/protocol'

/**
 * TradingView 指标「Doji X + Blue ZigZag」的浏览器端移植（原脚本见
 * `pine/Doji X + Blue ZigZag.pine`，Pine Script v4）。
 *
 * 这个文件只做**一件事**：把 Pine 里那 10 行算式逐句翻成 TS，产出「哪些 K 线上有蓝点、
 * 哪些 K 线上有 ✖」。画不画、画成什么样在 render.ts 里，这里不碰 canvas。
 *
 * 为什么不放后端（EMA20 就是在后端算的）：
 *   - EMA20 是**盘面事实**，Terminal / 网页 / 别的界面必须给同一个数；
 *   - 这两个参数（Lag / Doji Size）是用户在工具栏上随手拧的**显示参数**，拧一下就变，
 *     走一遍协议回来会让「拖一下输入框」变成一次网络往返，而且它们不改盘面、不进存档。
 * 所以它和缩放、平移一样留在前端；口径由这里唯一一份代码钉死。
 *
 * 三条和 Pine 对齐时容易写错的地方，都在下面逐个标了：
 *   1. `ema()` 的种子是**前 n 根的简单平均**，不是第一根的收盘价（Pine 从 v4 起就是 SMA 种子）；
 *   2. `mom(x, n)` 里 n 是 `round(lookback / 2)`，是**半步**，不是 lookback；
 *   3. `plot(... ? ll : ... ? hh : na)` 的 na 会让折线断开 —— 原脚本画面上是**散落的点**。
 *      这个文件只负责「点在哪」，把点连成一条 ZigZag 是 render.ts 里加的一层画法，
 *      不参与任何价格计算（连线不改价格、不改点的位置）。
 */

/** 指标参数。就是 Pine 脚本顶部那两个 `input()`，改一个就重画，不发给后端。 */
export interface IndicatorSettings {
  /** 总开关：关掉就一点不画（参数仍然留着，下次打开还是这一套） */
  show: boolean
  /** Pine 的 `lookback`：EMA 周期，同时是 ZigZag 高低点的回看窗口（2..50） */
  lookback: number
  /** Pine 的 `dojiSize`：实体 ≤ 振幅 × 这个比例就算十字星（0.01..1） */
  dojiSize: number
}

/** 出厂值 = 脚本里 `input()` 的默认值，和 TradingView 上刚加到图上时完全一致。 */
export const DEFAULT_INDICATOR: IndicatorSettings = {
  show: true,
  lookback: 6,
  dojiSize: 0.15,
}

/**
 * 夹到合法区间。
 *
 * 输入框可以被手输成 0、负数、`abc`（`Number('abc')` 是 NaN）—— 一个 NaN 的 lookback
 * 会让整条指标静默消失，用户只会觉得「开关坏了」。所以所有入口都过这一道。
 */
export function sanitizeIndicator(s: IndicatorSettings): IndicatorSettings {
  const lookback = Number.isFinite(s.lookback) ? Math.round(s.lookback) : DEFAULT_INDICATOR.lookback
  const dojiSize = Number.isFinite(s.dojiSize) ? Math.round(s.dojiSize * 100) / 100 : DEFAULT_INDICATOR.dojiSize
  return {
    show: s.show !== false,
    lookback: Math.min(50, Math.max(2, lookback)),
    dojiSize: Math.min(1, Math.max(0.01, dojiSize)),
  }
}

/** 一个蓝点：某根 K 线的最低点（crossover）或最高点（crossunder）。 */
export interface ZigzagPoint {
  index: number
  price: number
  /** low = 动量上穿 0（画在最低点）；high = 动量下穿 0（画在最高点） */
  kind: 'low' | 'high'
}

/** 一个 ✖：`above` = 阳线上方（Pine 的 location.abovebar），false = 阴线下方。 */
export interface DojiMark {
  index: number
  above: boolean
}

export interface IndicatorSeries {
  zigzag: ZigzagPoint[]
  doji: DojiMark[]
}

const EMPTY: IndicatorSeries = { zigzag: [], doji: [] }

/**
 * 缓存：Canvas 每帧都会来要一次，而 EMA 要扫全量 K 线。
 *
 * 键用**数组本身**（WeakMap）：`instance-reducer` 步进时是 `inst.bars = [...inst.bars, bar]`
 * 整个换新数组，所以「换了数组 = 多了一根」天然成立，不用自己数版本号。
 * 同一个数组再配 `bars.length` 兜底（回放/快照里万一原地 push 也不会拿到旧结果）。
 */
const cache = new WeakMap<Bar[], { length: number; lastTime: string; lookback: number; dojiSize: number; result: IndicatorSeries }>()

/** 入口：给定已揭示的 K 线和参数，算出全部蓝点与 ✖。 */
export function indicatorSeries(bars: Bar[], lookback: number, dojiSize: number): IndicatorSeries {
  if (bars.length === 0) return EMPTY
  const n = Math.max(2, Math.round(lookback))
  const lastTime = bars[bars.length - 1].time
  const hit = cache.get(bars)
  if (hit && hit.length === bars.length && hit.lastTime === lastTime && hit.lookback === n && hit.dojiSize === dojiSize) {
    return hit.result
  }
  const result = compute(bars, n, dojiSize)
  cache.set(bars, { length: bars.length, lastTime, lookback: n, dojiSize, result })
  return result
}

/**
 * Pine: `ema(src, length)`。
 *
 * 关键在 **na 的处理**：Pine 的 ema 会跳过 na，用「前 length 个**有值的**样本」做 SMA 种子。
 * 这一点在内层 `ema(ema(close, n), n)` 上是要命的 —— 第一层 EMA 的前 n-1 根是 na，
 * 如果第二层老老实实把开头的 na 加进种子里，整个数组会一路 NaN 到底（实测：蓝点一个都不出来）。
 * 所以先把有值的样本连下标一起抽出来算，再按原下标摊回去。
 */
function ema(src: number[], length: number): number[] {
  const out = new Array<number>(src.length).fill(Number.NaN)
  const vals: number[] = []
  const at: number[] = []
  for (let i = 0; i < src.length; i += 1) {
    if (Number.isFinite(src[i])) {
      vals.push(src[i])
      at.push(i)
    }
  }
  if (vals.length < length) return out

  let sum = 0
  for (let i = 0; i < length; i += 1) sum += vals[i]
  let prev = sum / length
  out[at[length - 1]] = prev

  const k = 2 / (length + 1)
  for (let j = length; j < vals.length; j += 1) {
    prev = vals[j] * k + prev * (1 - k)
    out[at[j]] = prev
  }
  return out
}

/** Pine: `mom(src, n)` = src - src[n]；任一端是 na 结果就是 na。 */
function mom(src: number[], n: number): number[] {
  const out = new Array<number>(src.length).fill(Number.NaN)
  for (let i = n; i < src.length; i += 1) {
    const a = src[i]
    const b = src[i - n]
    if (Number.isFinite(a) && Number.isFinite(b)) out[i] = a - b
  }
  return out
}

/** Pine: `lowest(low, n)` / `highest(high, n)`，窗口含当前这根；不足 n 根时是 na。 */
function windowExtreme(bars: Bar[], index: number, n: number, high: boolean): number {
  if (index < n - 1) return Number.NaN
  let best = high ? -Number.POSITIVE_INFINITY : Number.POSITIVE_INFINITY
  for (let i = index - n + 1; i <= index; i += 1) {
    const b = bars[i]
    if (!b) return Number.NaN
    best = high ? Math.max(best, b.high) : Math.min(best, b.low)
  }
  return best
}

function compute(bars: Bar[], lookback: number, dojiSize: number): IndicatorSeries {
  const zigzag: ZigzagPoint[] = []
  const doji: DojiMark[] = []

  // ---- Blue ZigZag ----
  const closes = bars.map((b) => b.close)
  const dema = ema(ema(closes, lookback), lookback)
  const half = Math.max(1, Math.round(lookback / 2))
  const mom1 = mom(dema, half)
  const mom2 = mom(mom1, half)

  for (let i = 1; i < bars.length; i += 1) {
    const cur = mom2[i]
    const prev = mom2[i - 1]
    if (!Number.isFinite(cur) || !Number.isFinite(prev)) continue
    // Pine 的 crossover/crossunder 比的是「上一根 ≤ 0 且这一根 > 0」，不是「这一根刚好是第一个正数」
    const over = cur > 0 && prev <= 0
    const under = cur < 0 && prev >= 0
    if (!over && !under) continue
    if (over) {
      const price = windowExtreme(bars, i, lookback, false)
      if (Number.isFinite(price)) zigzag.push({ index: i, price, kind: 'low' })
    } else {
      const price = windowExtreme(bars, i, lookback, true)
      if (Number.isFinite(price)) zigzag.push({ index: i, price, kind: 'high' })
    }
  }

  // ---- Doji X ----
  // Pine: doji = |o-c| <= (h-l) * dojiSize；方向由 o-c 的符号定，**正好相等（o == c）不画**。
  // 那一条不是漏洞：脚本里两个 plotchar 分别判 < 0 和 > 0，完全相等的十字星两边都不亮。
  for (let i = 0; i < bars.length; i += 1) {
    const b = bars[i]
    const body = Math.abs(b.open - b.close)
    const range = b.high - b.low
    if (body > range * dojiSize) continue
    if (b.close > b.open) doji.push({ index: i, above: true })
    else if (b.close < b.open) doji.push({ index: i, above: false })
  }

  return { zigzag, doji }
}
