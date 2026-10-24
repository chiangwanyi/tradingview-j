/**
 * 月历的**日期算术**：把「YYYY-MM」摊成 42 个格子（6 行 × 7 列），以及前后翻页。
 *
 * 为什么单独一个文件：这些函数全是纯计算，和 Vue、和交易记录都无关 ——
 * 混进组件里以后，「这个月为什么从周二开始」这种问题就要翻模板才能回答。
 *
 * 两条刻意的约定：
 *
 *  1. **一律用本地时间的日历日**，不碰 UTC。交易时间本身就是「K 线那根柱子的
 *     明文时间」，用 `new Date('2026-10-24')` 那种 UTC 解析会在东八区把
 *     某些日期整整错一天 —— 表现成「日历上少一格 / 多一格」，很难查。
 *     所以字符串进、字符串出，中间只在构造 Date 时拆成三个数字。
 *  2. **一周从周一开始**（中文习惯），格子固定 42 个。固定行数是为了让月份
 *     切换时整块日历不跳高度；不属于本月的格子照样渲染，只是置灰不可点。
 */

/** 星期的表头（周一起） */
export const WEEKDAYS = ['一', '二', '三', '四', '五', '六', '日'] as const

/** 一个格子。 */
export interface CalendarCell {
  /** `YYYY-MM-DD` */
  day: string
  /** 几号（格子左上角那个数字） */
  date: number
  /** 是不是属于当前这个月：不属于的置灰、不可点 */
  inMonth: boolean
  /** 是不是今天 */
  isToday: boolean
}

const pad2 = (n: number): string => String(n).padStart(2, '0')

/** `Date` → `YYYY-MM-DD`（本地日历日）。 */
export function dateKey(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

/** 今天，`YYYY-MM-DD`。 */
export function todayKey(): string {
  return dateKey(new Date())
}

/** `YYYY-MM-DD` → `Date`（本地零点）。字符串拆成数字再构造，避开 UTC 解析。 */
export function parseDay(day: string): Date {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(y, (m || 1) - 1, d || 1)
}

/** `YYYY-MM-DD` 属于哪个月：`YYYY-MM`。 */
export function monthOf(day: string): string {
  return day.slice(0, 7)
}

/** 月份加减（跨年自动进位）。 */
export function shiftMonth(month: string, delta: number): string {
  const [y, m] = month.split('-').map(Number)
  const d = new Date(y, m - 1 + delta, 1)
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`
}

/** 日期加减（按自然日）。 */
export function shiftDay(day: string, delta: number): string {
  const d = parseDay(day)
  d.setDate(d.getDate() + delta)
  return dateKey(d)
}

/** `2026-10` → `2026年10月`。 */
export function monthLabel(month: string): string {
  const [y, m] = month.split('-').map(Number)
  return `${y}年${m}月`
}

/** `2026-10-24` → `2026年10月24日 周六`。 */
export function dayLabel(day: string): string {
  const d = parseDay(day)
  const week = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][d.getDay()]
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 ${week}`
}

/**
 * 把一个月摊成 42 个格子（周一起）。
 *
 * today 传进来而不是在里面现取：这样「今天」在整块日历里只有一个来源，
 * 也方便测试固定某一天。
 */
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
