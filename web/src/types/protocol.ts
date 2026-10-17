/**
 * 前后端 JSON 协议的类型镜像。
 *
 * 对面在 internal/replay/protocol.go —— **两边要一起改**。这里刻意不做类型体操
 * （不搞 discriminated union 的深度推导）：协议是要能对着 Go 结构体一行行核对的，
 * 越花哨越难核对。事件的 payload 按 action 用具体类型断言即可。
 *
 * 时间一律是 CSV 里那一列的原始文本（"2026-10-16 12:00"），不用时间戳、不换算时区。
 */

export interface Bar {
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  /** 前 19 根没有值（Go 里是 *float64，序列化成 null） */
  ema20: number | null
  /** 从本次加载的第一根起算的序号，从 1 开始 */
  barCount: number
}

export interface Point {
  time: string
  price: number
}

export type AnnotationKind = 'line' | 'rect' | 'text'

export interface Annotation {
  id: string
  kind: string
  start: Point
  end: Point
  text?: string
  color?: string
  snapped: boolean
  createdBy: string
  createdAt: number
}

export type Side = 'buy' | 'sell'
export type OrderKind = 'limit' | 'stop'
export type OrderStatus = 'open' | 'filled' | 'cancelled'

export interface Order {
  id: string
  side: Side
  kind: OrderKind
  price: number
  quantity: number
  status: OrderStatus
  placedAt: string
  placedIndex: number
  filledAt?: string
  filledIndex: number
  fillPrice?: number
}

export interface Trade {
  id: string
  orderId: string
  time: string
  side: Side
  kind: OrderKind
  price: number
  quantity: number
  realizedPnl: number
}

export interface Position {
  quantity: number
  side: 'long' | 'short' | 'flat'
  averagePrice: number
}

export interface Member {
  clientId: string
  name: string
  operator: boolean
  connected: boolean
}

export interface ReplayEvent {
  seq: number
  at: number
  by: string
  action: string
  payload?: unknown
}

export interface InstanceDetail {
  id: string
  symbol: string
  folder: string
  anchor: string
  cursor: number
  bars: Bar[]
  hiddenBars: number
  hasMore: boolean
  annotations: Annotation[]
  orders: Order[]
  trades: Trade[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
  operator: string
  members: Member[]
  bot: boolean
  seq: number
  events: ReplayEvent[]
  notes: string[]
  createdBy: string
  createdAt: number
}

export interface InstanceSummary {
  id: string
  symbol: string
  anchor: string
  cursor: number
  loadedBars: number
  members: number
  operator: string
  bot: boolean
  createdBy: string
  createdAt: number
  realizedPnl: number
  trades: number
}

export interface SymbolInfo {
  symbol: string
  folder: string
  days: number
  firstDate: string
  lastDate: string
}

export interface You {
  clientId: string
  name: string
  operator: boolean
}

// ---------------- 下行消息 ----------------

export interface WelcomeMessage {
  type: 'welcome'
  clientId: string
  name: string
  version: string
}

export interface ErrorMessage {
  type: 'error'
  message: string
  request?: string
  instanceId?: string
}

export interface SymbolsMessage {
  type: 'symbols'
  symbols: SymbolInfo[]
}

export interface InstancesMessage {
  type: 'instances'
  instances: InstanceSummary[]
}

export interface InstanceStateMessage {
  type: 'instance_state'
  you: You
  instance: InstanceDetail
}

export interface ServerEvent {
  type: 'event'
  instanceId: string
  seq: number
  at: number
  by: string
  action: string
  payload?: unknown
}

export interface LeftMessage {
  type: 'left'
  instanceId: string
}

export interface DeletedMessage {
  type: 'deleted'
  instanceId: string
}

export interface PongMessage {
  type: 'pong'
}

export type ServerMessage =
  | WelcomeMessage
  | ErrorMessage
  | SymbolsMessage
  | InstancesMessage
  | InstanceStateMessage
  | ServerEvent
  | LeftMessage
  | DeletedMessage
  | PongMessage

// ---------------- 广播负载 ----------------

export interface BarAdvancedPayload {
  bar: Bar
  cursor: number
  hiddenBars: number
  hasMore: boolean
  orders: Order[]
  trades: Trade[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
  fills: Trade[]
}

export interface AnnotationPayload {
  annotation: Annotation
}

export interface AnnotationDeletedPayload {
  annotationId: string
}

export interface OrdersPayload {
  orders: Order[]
}

export interface ControlChangedPayload {
  operator: string
}

export interface MembersChangedPayload {
  members: Member[]
}

export interface BotPayload {
  intervalMs: number
}

// ---------------- 上行消息 ----------------

/**
 * 上行消息的形状。Go 那边只用一个 Request 结构体接（见 protocol.go 的说明），
 * 所以这里也不做逐类型的严格联合 —— 但每个 action 的构造集中在 stores/replay.ts 里，
 * 不散落在组件中。
 */
export type ClientMessage = { type: string } & Record<string, unknown>

export const ACTION = {
  barAdvanced: 'bar_advanced',
  annotationAdded: 'annotation_added',
  annotationMoved: 'annotation_moved',
  annotationDeleted: 'annotation_deleted',
  orderPlaced: 'order_placed',
  orderCancelled: 'order_cancelled',
  controlChanged: 'control_changed',
  membersChanged: 'members_changed',
  botStarted: 'bot_started',
  botStopped: 'bot_stopped',
} as const

/** 标注颜色：默认给一个中性色，工具条上可选。 */
export const ANNOTATION_COLORS = ['#f59e0b', '#38bdf8', '#f472b6', '#a3e635'] as const
