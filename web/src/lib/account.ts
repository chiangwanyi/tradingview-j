/**
 * 考核账户在界面上的文案（v0.31.0）。
 *
 * 只做「数字 → 中文」，不做任何算术：余额、亏损线、当日净额的口径都在后端，
 * 前端自己算一次就必然和日终结算对不上。
 */

import type { AccountPhase } from '@/types/protocol'

/** 阶段徽标上的字。breached 用「失败」而不是「爆仓」，因为它是规则判定的结果。 */
export function phaseLabel(phase: string): string {
  switch (phase) {
    case 'eval':
      return 'EVAL'
    case 'funded':
      return 'FUNDED'
    case 'payout':
      return 'PAYOUT'
    case 'breached':
      return '失败'
    default:
      return phase
  }
}

/** 阶段徽标的配色：EVAL 蓝、FUNDED 绿、PAYOUT 金、失败 红。 */
export function phaseClass(phase: string): string {
  switch (phase) {
    case 'funded':
      return 'bg-emerald-500/15 text-emerald-700'
    case 'payout':
      return 'bg-amber-500/15 text-amber-700'
    case 'breached':
      return 'bg-red-500/15 text-red-600'
    default:
      return 'bg-sky-500/15 text-sky-700'
  }
}

/** 金额：两位小数 + 千分位，负数带符号。 */
export function money(v: number): string {
  const sign = v < 0 ? '-' : ''
  return `${sign}${Math.abs(v).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

/** 带符号的盈亏（正数前面补 +，方便一眼分红绿）。 */
export function signedMoney(v: number): string {
  return `${v > 0 ? '+' : ''}${money(v)}`
}

export type { AccountPhase }
