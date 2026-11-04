/**
 * 行情接口：`/api/bars`（Spring 侧的 `BarController`，只读）。
 *
 * 只有两个请求：标的清单、一段 K 线（上一交易日整天 → 请求给的时刻）。没有写接口 ——
 * K 线是抓取程序写的 CSV，服务端只负责按「标的 + 截止时刻」读出来，前端不做任何保存。
 */

import { call } from './api'

/** 标的选择器里的一行。tick / tickValue 没配置时后端会把字段整个省掉（`non_null`）。 */
export interface SymbolBrief {
  code: string
  folder: string
  tick?: number
  tickValue?: number
  commission?: number
  /** 磁盘上有多少个日文件 */
  days: number
  /** 最早 / 最新的日文件日期；一个文件都没有时为空 */
  firstDay?: string
  lastDay?: string
}

/** CSV 那一行，时间不换算时区（画布的横坐标就是这一列）。 */
export interface RawBar {
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface BarSeries {
  code: string
  folder: string
  /** 截止那一天（`yyyy-MM-dd`）；请求没给 time 时是磁盘上最新的一天 */
  day: string
  tick?: number
  tickValue?: number
  /** 第一根可能来自上一个交易日；休市 / 没有文件的那一天是空数组，不是错误 */
  bars: RawBar[]
}

export const symbols = (): Promise<SymbolBrief[]> => call('/api/bars/symbols')

/**
 * 从**上一个交易日整天**开始、到 `time` 那一刻为止的 K 线。
 *
 * `time` 是本地明文时刻 `yyyy-MM-dd HH:mm`（如 `2025-12-04 09:00`）。上一个交易日
 * 由后端按磁盘上的日文件算（不是日历减一天，周末和休市日没有文件），前端不用关心。
 * 不传 `time` = 磁盘上最新的一天整天。
 */
export const series = (code: string, time?: string): Promise<BarSeries> => {
  const q = new URLSearchParams({ code })
  if (time) q.set('time', time)
  return call(`/api/bars?${q.toString()}`)
}
