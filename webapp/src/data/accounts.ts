/**
 * PF 账户的接口与类型。字段对齐后端
 * `controller/PropFirmAccountController.java` + `entity/propfirm/PropFirmAccount.java`。
 *
 * 前端不自己算账：余额、阶段、规则快照全部由后端给，这里只做展示和发请求。
 *
 * 后端 `default-property-inclusion: non_null` 会把取值为 null 的字段整个省掉，
 * 所以拿到的对象里读到的是 `undefined` —— 判断一律写 `== null`。
 */

import { call, send } from './api'
import type { Rules, Stage } from './rules'

const BASE = '/api/pf-accounts'

export type AccountStatus = 'ACTIVE' | 'FAILED'

/** 一次考核。`stage` 是当前阶段，`status` 只区分「还在」和「考挂了」。 */
export interface Account {
  id: string
  name: string
  /** 从哪套模板开的（只用于追溯；真正生效的是下面的 rules 快照）。 */
  templateId: string
  stage: Stage
  status: AccountStatus
  /** 开账户那一刻从模板复制过来的规则，之后与模板无关。 */
  rules: Rules
  /** 本阶段起点余额（升级时重置回账户规模）。 */
  baseBalance: number
  /** 本阶段「日终余额」历史最高，跟踪回撤靠它。 */
  eodPeakBalance: number
  balance: number
  /** 进入当前阶段的时刻（ISO 字符串）。 */
  stageStartedAt: string
  /** 这行账户写进库的真实时刻。 */
  startedAt: string
  /** 模拟时间轴上的起点 K 线，如 "2024-03-04 09:00"。 */
  startBarTime: string
  startDayFile: string
}

/** 阶段流水：这个账户当过哪几个阶段的什么身份。行数就说明身份。 */
export interface StageLog {
  id: string
  accountId: string
  seq: number
  stage: Stage
  startedAt: string
  endedAt: string | null
  baseBalance: number
  balanceAtEnd: number | null
  netPnl: number | null
  maxDailyProfit: number | null
  profitDays: number | null
  reason: 'CREATED' | 'PROMOTED'
  endReason: 'PROMOTED' | 'FAILED' | 'PAYOUT_DONE' | null
}

export const list = (status?: AccountStatus): Promise<Account[]> =>
  call(`${BASE}${status ? `?status=${status}` : ''}`)

export const open = (templateId: string, name: string): Promise<Account> =>
  call(BASE, send('POST', { templateId, name }))

export const rename = (id: string, name: string): Promise<Account> =>
  call(`${BASE}/${id}`, send('PUT', { name }))

export const remove = (id: string): Promise<null> => call(`${BASE}/${id}`, { method: 'DELETE' })

/** 升级到下一阶段。EVAL → FUNDED 会换规则、余额重置；FUNDED → PAYOUT 是终点。 */
export const promote = (id: string): Promise<Account> =>
  call(`${BASE}/${id}/promote`, { method: 'POST' })

/** 判失败（撞了亏损线）。终态，不会自动重开。 */
export const fail = (id: string): Promise<Account> => call(`${BASE}/${id}/fail`, { method: 'POST' })

export const stages = (id: string): Promise<StageLog[]> => call(`${BASE}/${id}/stages`)

/** 卡片上的进度：本阶段已赚到目标的几成。没有盈利目标（FUNDED / PAYOUT）返回 null。 */
export function targetProgress(a: Account): number | null {
  const target = a.rules.profitTarget
  if (target == null || target <= 0) return null
  const gained = a.balance - a.baseBalance
  return Math.min(1, Math.max(0, gained / target))
}

/** "2024-03-04 09:00" → "2024-03-04 09:00"，截到分钟，年月日全留（跨年账户要能分辨）。 */
export function shortTime(barTime: string): string {
  return barTime.length >= 16 ? barTime.slice(0, 16) : barTime
}

/** ISO → "YYYY-MM-DD HH:mm"（本地时区）。解析不了就原样给回去，别显示 Invalid Date。 */
export function shortInstant(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
