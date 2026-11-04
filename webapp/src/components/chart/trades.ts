import type { ClosedLot } from '@/types/protocol'

/**
 * 画布上「一根平仓虚线 = 一批被平掉的仓位」的展示类型。
 *
 * ⚠️ **数据来自后端，不是前端算的**（`Instance.closedLots`，协议字段 `closedLots`）。
 *
 * 曾经在前端做 FIFO 配对 + 把后端那一笔的总盈亏按手数摊给各批，结果是**错的**：
 * 5 笔空单分批开在 7076.14 / 7081 / 7083.25 / 7081.5 / 7082.5，一起在 7079.25 平掉，
 * 后端按平均成本算出这一笔总共 +40.70；摊成 5 份后每条线都显示 +8.14 ——
 * 既抹掉了各批开仓价的差别，还把开在平仓价之下、明明亏钱的那一笔显示成盈利。
 *
 * 现在每一批的盈亏都由后端**用它自己的开仓价**算好（`(平仓价-开仓价)×手数×方向×每点价值`），
 * 前端一处乘法都不做 —— 与「账全在 Go 算」的规矩一致。顺带保证：
 * 画线、命中测试、信息卡读的是同一份数据，不会各算各的。
 */
export type ClosedTrade = ClosedLot

/**
 * 一条平仓线的唯一键（只用于「悬停时高亮哪一条」）。
 *
 * 用下标不行：平仓批次每次成交后都会重算，同一个下标可能换成另一笔交易；
 * 用这一个字符串就能在重算前后对上同一批（同一开仓时间 + 同一开仓价 + 同一平仓价 + 同一手数）。
 */
export function closedKey(c: ClosedTrade): string {
  return `${c.openTime}|${c.openPrice}|${c.closeTime}|${c.closePrice}|${c.quantity}`
}

/**
 * 持仓时长：平仓时间 - 开仓时间，格式固定「x小时x分钟」。
 *
 * 时间字符串是 CSV 里的原始文本 `yyyy-MM-dd HH:MM`（本地时区）。这里按**日历**解析
 * （年/月/日/时/分逐段喂给 Date），不走 `Date.parse("2026-10-16 09:35")` ——
 * 带空格的这种格式在部分浏览器里会被当成 UTC，甚至直接 NaN。
 *
 * 不足 1 分钟按 0 分钟算（四舍五入到整分）；超过一天也只报小时（`30小时20分钟`），
 * 不做「1天6小时」的换算 —— 复盘横跨几天时，小时数最直观。
 */
export function durationText(from: string, to: string): string {
  const a = minuteOf(from)
  const b = minuteOf(to)
  if (a === undefined || b === undefined) return '—'
  const minutes = Math.max(0, Math.round(b - a))
  return `${Math.floor(minutes / 60)}小时${minutes % 60}分钟`
}

function minuteOf(s: string): number | undefined {
  if (s.length < 16) return undefined
  const y = Number(s.slice(0, 4))
  const mo = Number(s.slice(5, 7))
  const d = Number(s.slice(8, 10))
  const hh = Number(s.slice(11, 13))
  const mm = Number(s.slice(14, 16))
  if (!(y > 1970) || !(mo >= 1 && mo <= 12) || !(d >= 1 && d <= 31)) return undefined
  if (!(hh >= 0 && hh <= 23) || !(mm >= 0 && mm <= 59)) return undefined
  return new Date(y, mo - 1, d, hh, mm).getTime() / 60000
}