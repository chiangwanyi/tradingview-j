import type { Bar } from '@/types/protocol'

/**
 * ATR（Average True Range）—— TradingView 内置指标 `Average true range` 的浏览器端移植。
 *
 * 对标的脚本（Pine v6）就两行算式：
 *
 *     plot(ma_function(ta.tr(true), length), title = "ATR")
 *
 * 也就是说 ATR = 「真实波幅」经过「四种平滑之一」。这里把整件事拆成两步：
 *   1. `trueRange()` —— TR 序列，口径和 Pine 的 `ta.tr(true)` 逐点对齐；
 *   2. `smooth()` —— 四种平滑（RMA / SMA / EMA / WMA），口径和 `ta.rma / ta.sma /
 *      ta.ema / ta.wma` 逐点对齐。
 *
 * **为什么从后端搬到前端（v0.23.0）**：
 *   - v0.22.0 时 ATR 是写死的 14 周期 + Wilder 平滑，作为「盘面事实」由 Go 算好下发；
 *   - 现在 Length 和 Smoothing 是用户在【指标 ▾】里随手拧的参数 —— 和 Doji X + Blue ZigZag
 *     的 Lag / Doji Size 属于同一类东西（见 indicators.ts 开头那段）。拧一下就走一趟协议
 *     回来才出结果，输入框就会「慢半拍」；而且参数组合是无穷的，后端不可能预先算好几套。
 *   - 所以整份实现只留这一处：前端算、前端画。Go 那边的固定 ATR14 一并删掉了 ——
 *     留着就是两份口径，迟早会有人问「为什么网页和记录里的 ATR 不一样」。
 *
 * 数值上等价：K 线按「同一条序列的前缀」逐步揭示，而 ATR 只依赖**已经走过的** K 线，
 * 所以在可见的这 400 多根上重算，结果和「拿全量数据算完再截断」完全一样。
 *
 * 四条和 Pine 对齐时容易写错的地方，都在下面逐个标了：
 *   1. `ta.tr(true)` 的第一根 TR 是 `high - low`（`true` 就是「不要 na，用当根振幅」）；
 *   2. 之后每根 TR = max(高-低, |高-前收|, |低-前收|) —— 跳空只能靠后两项露出来；
 *   3. 四种平滑的**种子都是前 length 个样本的简单平均**（连 WMA 也是按权重算满 length 根，
 *      不是从第一根就能出值），所以整条线在第 length 根（下标 length-1）才有第一个点；
 *   4. RMA 的 alpha 是 1/length，**不是** EMA 的 2/(length+1)。两者差得不多但确实不同。
 */

/** 四种平滑方式。字符串就是 TradingView 下拉框里显示的那个名字，顺手当协议/存档里的标识。 */
export type AtrSmoothing = 'RMA' | 'SMA' | 'EMA' | 'WMA'

/** 下拉框的选项顺序 = Pine 脚本里 `options` 的顺序（RMA 打头，因为它是默认值）。 */
export const ATR_SMOOTHINGS: readonly AtrSmoothing[] = ['RMA', 'SMA', 'EMA', 'WMA']

/**
 * 指标参数。就是 Pine 脚本顶部那两个 `input()`：
 * `input.int("Length", 14, minval=1)` 和 `input.string("Smoothing", "RMA", ...)`。
 */
export interface AtrSettings {
  /** 总开关：关掉就整块副图让还价格区（参数仍然留着，下次打开还是这一套） */
  show: boolean
  /** Pine 的 `length`：平滑周期（1..1000） */
  length: number
  /** Pine 的 `smoothing`：RMA / SMA / EMA / WMA */
  smoothing: AtrSmoothing
}

/** 出厂值 = 脚本里 `input()` 的默认值，和 TradingView 上刚加到图上时完全一致。 */
export const DEFAULT_ATR: AtrSettings = {
  show: false,
  length: 14,
  smoothing: 'RMA',
}

/** 周期上限。没有功能上的必要，只是为了挡住手输的 99999（那样的线一根都画不出来）。 */
export const ATR_LENGTH_MAX = 1000

/**
 * 夹到合法区间。
 *
 * 和 `sanitizeIndicator` 同一个理由：输入框可以被手输成 0、负数、`abc`
 * （`Number('abc')` 是 NaN），一个 NaN 的 length 会让整条线静默消失，
 * 用户只会觉得「副图坏了」。所以所有入口（输入框、读存档、读 localStorage）都过这一道。
 */
export function sanitizeAtr(s: AtrSettings): AtrSettings {
  const raw = Number(s.length)
  const length = Number.isFinite(raw) ? Math.round(raw) : DEFAULT_ATR.length
  const smoothing = ATR_SMOOTHINGS.includes(s.smoothing) ? s.smoothing : DEFAULT_ATR.smoothing
  return {
    show: s.show === true,
    length: Math.min(ATR_LENGTH_MAX, Math.max(1, length)),
    smoothing,
  }
}

/** 副图左上角那行图例，例如 `ATR 14 RMA`（TradingView 也是这么标的）。 */
export function atrLegend(s: AtrSettings): string {
  return `ATR ${s.length} ${s.smoothing}`
}

/** 一个值「还没有」就记 NaN，画的时候据此断线（和 indicators.ts 里 ema() 的处理一致）。 */
const NA = Number.NaN

/**
 * 真实波幅序列，等价于 Pine 的 `ta.tr(true)`。
 *
 * `handle_na = true`（脚本里传的就是 `true`）：第一根没有前收，就用当根振幅顶上，
 * 而不是留 na —— 留 na 会让 RMA 的种子少一个样本，整条线整体右移一根。
 */
export function trueRange(bars: Bar[]): number[] {
  const out = new Array<number>(bars.length).fill(NA)
  for (let i = 0; i < bars.length; i += 1) {
    const b = bars[i]
    if (i === 0) {
      out[i] = b.high - b.low
      continue
    }
    const prevClose = bars[i - 1].close
    out[i] = Math.max(b.high - b.low, Math.abs(b.high - prevClose), Math.abs(b.low - prevClose))
  }
  return out
}

/**
 * 四种平滑，等价于 Pine 的 `ta.rma / ta.sma / ta.ema / ta.wma`。
 *
 * 四者都有同一件事：**前 length-1 根是 na**，第一根有值的落在下标 length-1。
 * 这条不统一的话，切换平滑方式时整条线会左右挪一格，看起来像是「换了参数数据也变了」。
 */
export function smooth(src: number[], length: number, kind: AtrSmoothing): number[] {
  const n = Math.max(1, Math.round(length))
  const out = new Array<number>(src.length).fill(NA)
  if (src.length < n) return out

  switch (kind) {
    case 'SMA':
      return sma(src, n)
    case 'EMA':
      return ema(src, n)
    case 'WMA':
      return wma(src, n)
    case 'RMA':
    default:
      return rma(src, n)
  }
}

/** `ta.sma`：滚动求和；前 n-1 根 na。 */
function sma(src: number[], n: number): number[] {
  const out = new Array<number>(src.length).fill(NA)
  let sum = 0
  for (let i = 0; i < src.length; i += 1) {
    sum += src[i]
    if (i >= n) sum -= src[i - n]
    if (i >= n - 1) out[i] = sum / n
  }
  return out
}

/**
 * `ta.rma`（Wilder 平滑）：种子是前 n 根的简单平均，之后 `prev + (x - prev) / n`。
 *
 * 和 EMA 的差别只在系数：这里是 1/n，EMA 是 2/(n+1)。n=14 时 1/14 ≈ 0.0714、
 * 2/15 ≈ 0.1333，差了快一倍 —— 所以两者画出来的线肉眼就能看出不一样，不能互换。
 */
function rma(src: number[], n: number): number[] {
  const out = new Array<number>(src.length).fill(NA)
  let sum = 0
  for (let i = 0; i < n; i += 1) sum += src[i]
  let prev = sum / n
  out[n - 1] = prev
  for (let i = n; i < src.length; i += 1) {
    prev += (src[i] - prev) / n
    out[i] = prev
  }
  return out
}

/** `ta.ema`：种子同样是前 n 根的简单平均（Pine 从 v4 起就如此），之后系数 2/(n+1)。 */
function ema(src: number[], n: number): number[] {
  const out = new Array<number>(src.length).fill(NA)
  let sum = 0
  for (let i = 0; i < n; i += 1) sum += src[i]
  let prev = sum / n
  out[n - 1] = prev
  const k = 2 / (n + 1)
  for (let i = n; i < src.length; i += 1) {
    prev = src[i] * k + prev * (1 - k)
    out[i] = prev
  }
  return out
}

/**
 * `ta.wma`：线性加权，最近一根权重最大（n, n-1, …, 1），分母是 n(n+1)/2。
 *
 * 用递推算，别每根都从头累加 —— 400 多根 × n 次乘法在每次重绘里都要跑一遍，拖参数会卡。
 * 递推式（W 是加权和、S 是「上一根为止的 n 根简单和」）：
 *
 *     W[i] = W[i-1] + n·x[i] - S[i-1]
 *
 * 验算：W[i] 比 W[i-1] 多出来的正好是「新一根拿满权重 n，旧的 n 根各自降一档」，
 * 降下去的那一份就是上一轮窗口里那 n 个值的和。
 */
function wma(src: number[], n: number): number[] {
  const out = new Array<number>(src.length).fill(NA)
  const denom = (n * (n + 1)) / 2

  // 种子：前 n 根直接按权重算一次（n 最大 1000，只跑这一次，不影响性能）。
  // 注意权重是**越新越大**：x[n-1] 拿 n，x[0] 拿 1。
  let sumPrev = 0
  let weighted = 0
  for (let j = 0; j < n; j += 1) {
    sumPrev += src[j]
    weighted += (j + 1) * src[j]
  }
  out[n - 1] = weighted / denom

  for (let i = n; i < src.length; i += 1) {
    weighted = weighted + n * src[i] - sumPrev
    sumPrev += src[i] - src[i - n]
    out[i] = weighted / denom
  }
  return out
}

/**
 * 缓存：Canvas 每帧都会来要一次，而 ATR 要扫全量 K 线。
 *
 * 键用**数组本身**（WeakMap）：实例步进时是 `inst.bars = [...inst.bars, bar]`
 * 整个换新数组，所以「换了数组 = 多了一根」天然成立。同一个数组再配 `bars.length`
 * 兜底（回放/快照里万一原地 push 也不会拿到旧结果），参数变了也重算。
 */
const cache = new WeakMap<
  Bar[],
  { length: number; lastTime: string; period: number; smoothing: AtrSmoothing; result: number[] }
>()

/**
 * 入口：给定已揭示的 K 线和参数，算出整条 ATR。取不到值的下标是 NaN。
 *
 * 返回整条数组而不是「当下这根的值」：副图要画折线、纵轴要按可见区间贴合，
 * 两处都要扫一段，各算一次不如一次算全。
 */
export function atrSeries(bars: Bar[], length: number, smoothing: AtrSmoothing): number[] {
  if (bars.length === 0) return []
  const n = Math.max(1, Math.round(length))
  const lastTime = bars[bars.length - 1].time
  const hit = cache.get(bars)
  if (
    hit &&
    hit.length === bars.length &&
    hit.lastTime === lastTime &&
    hit.period === n &&
    hit.smoothing === smoothing
  ) {
    return hit.result
  }
  const result = smooth(trueRange(bars), n, smoothing)
  cache.set(bars, { length: bars.length, lastTime, period: n, smoothing, result })
  return result
}
