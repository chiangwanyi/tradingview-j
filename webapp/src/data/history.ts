/**
 * 历史记录接口：`/api/history`（后端 `controller/HistoryController.java`）。
 *
 * 两个页面（操作记录 / 交易记录）都是**只读**的全量分页列表；唯一能写的动作是清空操作记录。
 * 分页从 0 起，和后面 `PageRequest` 一致 —— 界面上显示页码时才 +1。
 */

import { call } from './api'

const BASE = '/api/history'

export interface Paged<T> {
  items: T[]
  total: number
  /** 当前页，从 0 起。 */
  page: number
  size: number
}

/** 一条操作记录。`action` / `actor` 是小写协议串，和 WS 广播同一套写法。 */
export interface AuditRow {
  id: string
  accountId: string
  accountName?: string
  symbolId: string
  barTime: string
  seq: number
  actor: string
  action: string
  createdAt: string
}

/** 一笔交易记录的摘要。 */
export interface TradeRow {
  id: string
  accountName?: string
  symbolId: string
  side?: 'buy' | 'sell'
  quantity?: number
  entryBarTime?: string
  exitBarTime?: string
  entryPrice?: number
  exitPrice?: number
  /** 毛利（没扣手续费）。 */
  realizedPnl?: number
  barsHeld: number
  closed: boolean
  actor: string
}

const paged = <T>(path: string, page: number, size: number): Promise<Paged<T>> =>
  call(`${BASE}/${path}?page=${page}&size=${size}`)

export const auditEvents = (page: number, size: number) => paged<AuditRow>('audit-events', page, size)
export const tradeRecords = (page: number, size: number) => paged<TradeRow>('trade-records', page, size)

/** 清空全部操作记录，返回删掉几条。不可逆。 */
export const clearAuditEvents = (): Promise<number> =>
  call(`${BASE}/audit-events`, { method: 'DELETE' })

/** 动作名 → 中文。漏掉的取值退化成原串显示，别让它变成空白格。 */
export const ACTION_LABEL: Record<string, string> = {
  bar_advanced: '下一根',
  bar_extended: '延伸一根',
  annotation_added: '画图形',
  annotation_moved: '拖图形',
  annotation_styled: '换图形样式',
  annotation_deleted: '删图形',
  annotation_cleared: '清空图形',
  order_placed: '挂单',
  order_filled: '成交',
  order_moved: '改挂单价',
  order_cancelled: '撤单',
  flat_all: '一键退仓',
  control_changed: '换控制权',
  members_changed: '连接变化',
  bot_started: '启动机器人',
  bot_stopped: '停止机器人',
  voice_added: '录语音',
  voice_cleared: '清语音',
  recording_audio: '语音步',
  base: '交易记录起点',
}

/**
 * ISO → "YYYY-MM-DD HH:mm"（本地时区）。和 `data/accounts.ts` 的 `shortInstant` 同一口径，
 * 解析不了就原样返回，别显示 Invalid Date。
 */
export function shortInstant(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
