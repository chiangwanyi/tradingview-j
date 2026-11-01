/**
 * 月历的**日期算术**：把「YYYY-MM」摊成 42 个格子（6 行 × 7 列），以及前后翻页。
 *
 * 和 `web/src/lib/calendar.ts` 是同一份算法（那边是复盘日历）。没有跨包共享的通道，
 * 所以这里是一份拷贝 —— 改动时两边都要改，否则两个页面的月份会错开一格。
 *
 * 两条刻意的约定：
 *
 *  1. **一律用本地时间的日历日**，不碰 UTC。`new Date('2026-10-24')` 那种 UTC 解析
 *     在东八区会把某些日期整整错一天，表现成「日历上少一格 / 多一格」，很难查。
 *     所以字符串进、字符串出，中间只在构造 Date 时拆成三个数字。
 *  2. **一周从周一开始**（中文习惯），格子固定 42 个。固定行数是为了让月份切换时
 *     整块日历不跳高度。
 */

/** 星期的表头（周一起） */
export const WEEKDAYS = ['一', '二', '三', '四', '五', '六', '日'] as const

export interface CalendarCell {
  /** `YYYY-MM-DD` */
  day: string
  /** 几号（格子左上角那个数字） */
  date: number
  inMonth: boolean
  isToday: boolean
}

const pad2 = (n: number): string => String(n).padStart(2, '0')

/** `Date` → `YYYY-MM-DD`（本地日历日）。 */
export function dateKey(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

export function todayKey(): string {
  return dateKey(new Date())
}

/** `YYYY-MM-DD` → `Date`（本地零点）。 */
export function parseDay(day: string): Date {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(y, (m || 1) - 1, d || 1)
}

/** `YYYY-MM-DD` → `YYYY-MM` */
export function monthOf(day: string): string {
  return day.slice(0, 7)
}

export function shiftMonth(month: string, delta: number): string {
  const [y, m] = month.split('-').map(Number)
  const d = new Date(y, m - 1 + delta, 1)
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`
}

/** `2026-10` → `2026年10月` */
export function monthLabel(month: string): string {
  const [y, m] = month.split('-').map(Number)
  return `${y}年${m}月`
}

/** 42 个格子，周一起。today 由调用方传，整块日历只有一个「今天」。 */
export function monthGrid(month: string, today = todayKey()): CalendarCell[] {
  const [y, m] = month.split('-').map(Number)
  const first = new Date(y, m - 1, 1)
  // getDay(): 周日 = 0。换算成「周一 = 0」
  const offset = (first.getDay() + 6) % 7
  const start = new Date(y, m - 1, 1 - offset)
  const cells: CalendarCell[] = []
  for (let i = 0; i < 42; i++) {
    const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i)
    const key = dateKey(d)
    cells.push({
      day: key,
      date: d.getDate(),
      inMonth: monthOf(key) === month,
      isToday: key === today,
    })
  }
  return cells
}
