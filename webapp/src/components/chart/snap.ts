import type { Bar } from '@/types/protocol'

import type { ChartScale } from './scale'

/**
 * Ctrl 吸附：把指针所在位置的价格吸到**那根 K 线的 OHLC** 上。
 *
 * 单独一个文件是因为它有三处要用（十字光标、定稿端点、拖动端点），
 * 在 ChartPanel.vue 里各写一遍迟早会漂；而 .vue 里的函数没法在 node 里直接 import
 * 做断言（要过 Vue 编译器），纯函数放这里就能跑。
 *
 * **取舍必须和后端 `internal/replay/instance.go` 的 `nearestOHLC` 逐字一致**：
 * 从开盘价起比、四项顺序 O -> H -> L -> C、**严格更小才替换**（并列时保留先比到的）。
 * 两边不一致的表现是「单击之后图形跳一下」——这套功能里最难查的一类 bug，
 * 所以改动这里时必须同时看一眼那个 Go 函数。
 *
 * **返回形状的分工**（v0.10.0 修过一次真 bug，这里是那次留下的约束）：
 *   - `nearestOHLC(bar, price)` → **裸数字**。端点要的就是价格本身。
 *   - `nearestOHLCItem(bar, price)` / `snapAtCursor(...)` → 带 `item`。
 *     只有十字光标和左上角提示需要「吸到了开/高/低/收哪一个」。
 *
 * 以前只有一个返回 `SnapResult` 的 `nearestOHLC`，于是调用点很容易顺手写成
 * `nearestOHLC(bar, p).price`。而 `ChartPoint.price` 是可选字段，TS 不报错、
 * 结果是一个 `undefined` 的价格 —— 按住 Ctrl 画出来的线会歪、拖端点吸附也失效，
 * 而且只在真机上复现。现在两个函数分开且返回类型写死，这类错误当场被 TS 挡住。
 */

/** 吸到的是 O/H/L/C 里的哪一个 —— 界面要把它说出来，用户才知道「吸住了什么」 */
export type OHLCItem = 'open' | 'high' | 'low' | 'close'

export interface SnapResult {
  price: number
  item: OHLCItem
}

/**
 * 一次吸附的全部结果：吸到了**哪根 K 线**的**哪个价**。
 *
 * 带上 `index`（而不是只给价格）是因为十字光标要用它画竖线：吸附不能只把横线拉过去、
 * 竖线还留在指针那半根上，那样用户会以为「吸歪了」。
 */
export interface SnapHit extends SnapResult {
  /** 被吸的那根 K 线在 `scale.bars` 里的下标 */
  index: number
}

/** 四项的中文名（提示文案用；和后端没有关系） */
export const OHLC_LABEL: Record<OHLCItem, string> = {
  open: '开',
  high: '高',
  low: '低',
  close: '收',
}

/**
 * 在 O/H/L/C 里挑离 price 最近的那个（并列时按 O、H、L、C 顺序取第一个）。
 *
 * 返回**裸数字**，不是 SnapResult —— 要知道「吸到了哪一个」用 `snapAtCursor`。
 *
 * **显式写返回类型是刻意的**：调用方很容易顺手写成 `nearestOHLC(bar, p).price`
 * （把 `snapAtCursor` 的返回形状套上来），而 ChartPoint.price 是可选字段，
 * 那样 TS 不会报错、结果是一个 undefined 的价格 —— 画出来的线在 Ctrl 吸附下会歪，
 * 而且只在真机上复现。写死 `number` 之后这类错误当场被编译器挡住。
 */
export function nearestOHLC(bar: Bar, price: number): number {
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

/** `nearestOHLC` 的「吸到了哪一个」版本，供十字光标画提示用 */
export function nearestOHLCItem(bar: Bar, price: number): SnapResult {
  const value = nearestOHLC(bar, price)
  const item: OHLCItem =
    value === bar.open ? 'open' : value === bar.high ? 'high' : value === bar.low ? 'low' : 'close'
  return { price: value, item }
}

/**
 * 指针位置 -> 吸住的那个 OHLC；没有可吸的东西时返回 null。
 *
 * **这是「Ctrl 吸附」的唯一入口**：十字光标每一帧调它，ChartPanel 的价格提示也调它，
 * 定稿端点调的是它内部那个 `nearestOHLC`。三处走同一份，不可能出现
 * 「光标停在 100.25、点下去却落在 100.00」这种对不上的情况。
 *
 * 吸附用的是**光标下那根 K 线**（`barAtCursor`）；光标越过最后一根时，
 * 那一段一根 K 线都没有，吸的是被夹回来的最后一根 —— 和后端 `resolvePoint`
 * 拿「锚点那根」吸是同一个口径。
 */
export function snapAtCursor(scale: ChartScale, x: number, y: number): SnapHit | null {
  const hit = scale.barAtCursor(x)
  if (!hit) return null
  return { ...nearestOHLCItem(hit.bar, scale.priceAtY(y)), index: hit.index }
}
