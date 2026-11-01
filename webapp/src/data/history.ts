/**
 * 历史记录接口：`/api/history`（后端 `controller/HistoryController.java`）。
 *
 * 两个页面（操作记录 / 交易记录）都是**只读**的全量分页列表；
 * 能写的只有两个清空动作，各管各的表，互不牵连：
 *
 *   `DELETE /audit-events`  清 `audit_event`（我按过哪些按钮）
 *   `DELETE /trade-data`    清交易信息那 4 张表（我手上做过哪些单）
 *
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
  /** 这条记录承担的手续费（后端按平仓腿 / 开仓腿切好）。 */
  commission?: number
  /** 净盈亏 = 毛利 − 手续费；记录还没收尾时后端给 null，这里就是 undefined。 */
  netPnl?: number
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

/**
 * 清空交易信息的结果：4 张表各删了几行（后端 `ClearTradeDataResponse`）。
 *
 * 分表返回是为了让用户能核对范围 —— 「挂单 12 / 成交 8 / 记录 3 / 标签 5」
 * 一眼看出删的是不是他以为的那几张表。
 */
export interface ClearedTradeData {
  /** `trade_order`：挂单（含已成交 / 已撤的历史单）。 */
  orders: number
  /** `trade`：成交明细。 */
  trades: number
  /** `trade_record`：交易记录（复盘用的那一条摘要）。 */
  records: number
  /** `trade_record_tag`：记录上的自定义标签。 */
  tags: number
}

/**
 * 清空全部交易信息（挂单 / 成交 / 交易记录 / 记录标签 4 张表），不可逆。
 *
 * 不动操作记录、账户（含余额）、图形、语音、K 线。已经打开的交易页要刷新才会看到空盘面。
 */
export const clearTradeData = (): Promise<ClearedTradeData> =>
  call(`${BASE}/trade-data`, { method: 'DELETE' })

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
