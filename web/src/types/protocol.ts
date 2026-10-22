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
}

/**
 * 标注端点。`time` 是**锚点** K 线的时间（一定真实存在），`offset` 是从它往右延伸的根数：
 * 0/缺省 = 就在那根 K 线上，>0 = 越过最后一根已画出来的 K 线、延伸到右侧空白区。
 *
 * 后端 protocol.go 的 Point 是同一份（offset 为 0 时后端不发这个字段）。
 */
export interface Point {
  time: string
  /** 从 time 那根 K 线往右再走几根（>= 0） */
  offset?: number
  price: number
}

/**
 * 五种绘图工具。字符串就是协议里的 `kind`，和后端 annotation.go 的常量一一对应。
 *
 * 它们全部用「起点 + 终点」两个点描述：趋势线/射线/箭头线是线的两端，
 * 矩形是两个对角点，斐波那契回撤是价格区间的两端。
 */
export type AnnotationKind = 'trend' | 'ray' | 'rect' | 'arrow' | 'fib'

/** 线型（只有两种：点线在 1px 上和虚线看不出区别） */
export type DashStyle = 'solid' | 'dashed'

/** 斐波那契回撤的一条水平线。ratio 只认 -1 / 0 / 1 / 2，由后端固定住。 */
export interface FibLevel {
  ratio: number
  color: string
}

/**
 * 图形样式。**它是画下去那一刻从模板抄过来的快照**：之后改模板、删模板，
 * 已经画好的图形不会跟着变。
 */
export interface AnnotationStyle {
  color: string
  width: number
  dash: DashStyle
  fill?: string
  fillOpacity?: number
  levels?: FibLevel[]
}

export interface Annotation {
  id: string
  kind: string
  start: Point
  end: Point
  style?: AnnotationStyle
  /** 0.5.2 及以前的单一颜色，只在读旧存档时出现；新标注只用 style.color */
  color?: string
  snapped: boolean
  createdBy: string
  createdAt: number
}

/** 一个工具下面的一套样式预设（全局的，不属于某个实例）。 */
export interface Template {
  id: string
  kind: string
  name: string
  style: AnnotationStyle
  createdAt: number
}

export type Side = 'buy' | 'sell'
/** market = 市价单：下单即按当前收盘价成交，不会留在挂单列表里 */
export type OrderKind = 'limit' | 'stop' | 'market'
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
  /**
   * OCO 组：同一组最多成交一张，其余同根自动撤销。
   * 空 = 独立挂单（用户手下的都是这种）；机器人的止损/止盈共用一组。
   */
  group?: string
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

/**
 * 一批被平掉的仓位 = 画布上的一根平仓虚线。
 *
 * 配对和盈亏都由后端算好（`instance.go: closedLots`）：每一批用**自己的开仓价**算
 * `(平仓价 - 开仓价) × 手数 × 方向 × 每点价值`，不是把某一笔的总盈亏摊给各批。
 * 前端只排版，不做任何乘法。
 */
export interface ClosedLot {
  /** 开仓成交的 K 线时间（= 那个三角的横坐标） */
  openTime: string
  /** 开仓的真实成交价（三角的纵坐标） */
  openPrice: number
  closeTime: string
  closePrice: number
  /** 被平掉那批的方向：long = 买开的仓，short = 卖开的仓 */
  side: 'long' | 'short'
  /** 这一批被平掉的手数（部分平仓时小于开仓量） */
  quantity: number
  /** 这一批自己的已实现盈亏（美元） */
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
  /** 最小跳动（来自 _symbols.json），价格轴用它定小数位 */
  tick: number
  /** 1 tick 值多少美元（MNQ 0.5 / MES 1.25 / MGC 1 / CL 10）；0 = 没配置 */
  tickValue: number
  bars: Bar[]
  hiddenBars: number
  hasMore: boolean
  annotations: Annotation[]
  orders: Order[]
  trades: Trade[]
  /** 逐批平仓记录（画布上那些平仓虚线的数据源，盈亏已由后端算好） */
  closedLots: ClosedLot[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
  operator: string
  members: Member[]
  bot: boolean
  /** 机器人正在跑的策略 id（空 = 老式的随机演示机器人） */
  botStrategy?: string
  /** 策略每笔下多少手 */
  botQuantity?: number
  seq: number
  events: ReplayEvent[]
  notes: string[]
  createdBy: string
  createdAt: number
  /** 正在录制时的状态；没在录是 null/undefined */
  recording?: RecordingState | null
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
  /** 机器人正在跑的策略 id（空 = 随机演示机器人），列表里用来显示「机器人 · Caveman」 */
  botStrategy?: string
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
  /** 最小跳动：0.25 / 0.1 / 0.01（写在 _symbols.json 里，缺省按标的名兜底） */
  tick: number
  /** 1 tick 的美元价值（写在 _symbols.json 里，缺省按标的名兜底）；0 = 不知道 */
  tickValue: number
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
  /**
   * 这条广播被录成了第几步（0 / 缺省 = 没在录或这一步不记录）。
   *
   * v0.15.0 之前前端在收到 `bar_advanced` 时自己把步数 +1；改成一操作一步之后
   * 那样必然算错（画图、录音也各占一步），所以步号由服务端下发，界面只显示。
   */
  recStep?: number
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

/**
 * 模板全量列表。模板很少（每个工具几条），所以后端任何一次改动都广播整份列表。
 * `lastSavedId` 是刚存下来的那一条：新建时 id 由后端分配，靠它把新模板立刻选上。
 */
export interface TemplatesMessage {
  type: 'templates'
  templates: Template[]
  lastSavedId?: string
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
  | TemplatesMessage
  | StrategiesMessage
  | RecordingsMessage
  | RecordingMessage

// ---------------- 广播负载 ----------------

export interface BarAdvancedPayload {
  bar: Bar
  cursor: number
  hiddenBars: number
  hasMore: boolean
  orders: Order[]
  trades: Trade[]
  /** 逐批平仓记录（画布上那些平仓虚线的数据源，盈亏已由后端算好） */
  closedLots: ClosedLot[]
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

/**
 * 清空全部绘图。`count` 是删掉几个（操作记录里直接显示），`annotationIds` 是
 * 「具体删了哪几个」—— 只留数量的话，事后回看只知道有人清了 5 个，不知道是哪 5 个。
 */
export interface AnnotationClearedPayload {
  count: number
  annotationIds: string[]
}

export interface OrdersPayload {
  orders: Order[]
}

/** 改挂单价：多带一个「改完的那张」，操作记录里才能写清改的是谁、改成了多少 */
export interface OrderMovedPayload {
  order: Order
  orders: Order[]
}

/** 市价单成交：订单、成交、持仓、盈亏一次给全，前端不用再要快照 */
export interface OrderFilledPayload {
  order: Order
  trade: Trade
  orders: Order[]
  trades: Trade[]
  /** 逐批平仓记录（画布上那些平仓虚线的数据源，盈亏已由后端算好） */
  closedLots: ClosedLot[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
}

export interface ControlChangedPayload {
  operator: string
}

export interface MembersChangedPayload {
  members: Member[]
}

export interface BotPayload {
  intervalMs: number
  /** 策略 id；空 = 随机演示机器人 */
  strategy?: string
  /** 策略的中文名（只用于显示） */
  name?: string
  /** 策略每笔下多少手 */
  quantity?: number
}

// ---------------- 策略 ----------------

/**
 * 一个策略参数的描述。前端不解释这些含义，只负责把默认值列出来、
 * 让用户知道「跑之前有哪些旋钮」。
 */
export interface StrategyParam {
  key: string
  label: string
  default: number
  min: number
  max: number
  step: number
  help: string
}

/**
 * 一个可加载的策略。策略是**编译期注册**在后端的，运行期不会变，
 * 所以这条消息一次性下发全量、前端缓存着用。
 */
export interface StrategyInfo {
  id: string
  name: string
  summary: string
  params: StrategyParam[]
}

export interface StrategiesMessage {
  type: 'strategies'
  strategies: StrategyInfo[]
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
  annotationCleared: 'annotation_cleared',
  orderPlaced: 'order_placed',
  orderFilled: 'order_filled',
  orderMoved: 'order_moved',
  orderCancelled: 'order_cancelled',
  controlChanged: 'control_changed',
  membersChanged: 'members_changed',
  botStarted: 'bot_started',
  botStopped: 'bot_stopped',
  recordingStarted: 'recording_started',
  recordingStopped: 'recording_stopped',
  recordingAudio: 'recording_audio',
  recordingAudioCleared: 'recording_audio_cleared',
} as const

/**
 * 调色板：模板编辑器里的快捷色块。挑的都是「深浅两套配色下都看得清」的中间调 ——
 * 图形里存的是十六进制颜色，换主题时颜色不跟着变（它已经存到服务端了）。
 */
export const ANNOTATION_COLORS = [
  '#f59e0b',
  '#0ea5e9',
  '#ec4899',
  '#65a30d',
  '#a855f7',
  '#ef4444',
  '#e5e7eb',
] as const

// ---------------- 录制与回放（v0.11.0） ----------------

/**
 * 这个实例现在的录制状态。跟着实例快照下发，也随三条录制广播更新。
 *
 * 步数与录音数**都是服务端数的**：界面只是显示，不自己累加 ——
 * 否则「界面写第 12 步、文件里只有 11 段」这种问题永远查不清。
 *
 * 术语（v0.15.0）：**一步 = 一次操作**。点【下一根】、画一个图形、挂一张单、
 * 录一段音各占一步；一根 K 线上可以有很多步，所以还有 `barNo`。
 */
export interface RecordingState {
  active: boolean
  id?: string
  by?: string
  startedAt?: number
  /** 当前第几步（1 = 点录制时那一屏） */
  step: number
  /** 一共几步（含起点） */
  total: number
  /** 当前是第几根 K 线（1 起，第 1 根 = 起点那根） */
  barNo: number
  /** 当前这一步是什么操作（base / bar_advanced / annotation_added / recording_audio …） */
  kind?: string
  /** 第 1 步对应的 K 线时间 */
  startTime?: string
  /** 当前这一步对应的 K 线时间 */
  current?: string
  /** 整条录像已录几条（**不限**） */
  audios: number
  /** 当前**这根 K 线**上已录几条（HUD 显示的就是这个） */
  barAudios: number
  /** 同一根 K 线的上限（9） */
  maxBarAudios: number
  /** 当前这根 K 线上的录音清单（HUD 里列出来、预览、清空） */
  clips: AudioClip[]
}

/** 录像列表里的一行。 */
export interface RecordingSummary {
  id: string
  name: string
  symbol: string
  folder: string
  anchor: string
  endTime: string
  /**
   * 摘要里的计数（带 Count 是为了不和 Recording.steps 那个数组撞名）。
   *
   * `stepCount` 是**操作步数**（v0.15.0 起一步一个操作，所以比以前大得多），
   * 想知道录了多长看 `bars`（推进了几根 K 线）。
   */
  stepCount: number
  audioCount: number
  eventCount: number
  bars: number
  size: number
  createdBy: string
  createdAt: number
  endedAt: number
}

/** 录制起点那一刻的盘面。 */
export interface RecordingBase {
  cursor: number
  bars: Bar[]
  annotations: Annotation[]
  orders: Order[]
  trades: Trade[]
  closedLots: ClosedLot[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
}

/**
 * 一条被录下来的操作。
 *
 * `payload` 就是当时广播出去的那份 JSON（原样保存、原样回放）——
 * 不重新构造，任何「翻译一层」都会让回放和实时产生细微差异。
 */
export interface RecordingEvent {
  seq: number
  at: number
  /** human / human2 / bot */
  by: string
  action: string
  payload?: unknown
}

/**
 * 录像里的一步 —— **一步 = 一次真实操作**（v0.15.0）。
 *
 * 第 1 步是起点（`kind: 'base'`，没有事件）；之后每一条放行的广播开一步：
 * 点一次【下一根】、画一个图形、拖一次挂单价、录一段音，各占一步。
 *
 * 录音步（`kind: 'recording_audio'`）特殊：`events` 为空（不改盘面），
 * 那段音频在 `audio` 里。回放到它画面不动，但语音该响。
 */
export interface RecordingStep {
  step: number
  /** 这一步的真实发生时刻（Unix 毫秒） */
  at: number
  /** 'base' / 动作名 / 'recording_audio' */
  kind: string
  /** human / bot / system */
  by?: string
  /** 这一步发生时当前那根 K 线的时间 */
  time: string
  /** 这一步发生后的游标（跳步时自检用） */
  cursor: number
  /** 仅录音步：挂在这一步上的那一段 */
  audio?: AudioClip
  events: RecordingEvent[]
}

/** 一段挂在某根 K 线上的录音。`time` 是 K 线时间（存储键）。 */
export interface AudioClip {
  id: string
  step: number
  time: string
  durationMs: number
  /** 录像目录里的文件名（audio-N.wav），音频本身走 HTTP 取 */
  file: string
  size: number
  createdAt: number
}

/** 一条完整的录像：起始盘面 + 每一步的事件 + 录音。 */
export interface Recording extends RecordingSummary {
  /**
   * 录像格式：2 = 一步一个操作（v0.15.0 起），1 = 旧的一步一根 K 线。
   * 服务端只下发 2（旧格式在 scan / Load 处就被挡掉了），这个字段留着是为了
   * 排查时能一眼看出手里这份文件是哪一代。
   */
  format: number
  tick: number
  tickValue: number
  base: RecordingBase
  steps: RecordingStep[]
  audios: AudioClip[]
}

export interface RecordingsMessage {
  type: 'recordings'
  dir: string
  recordings: RecordingSummary[]
}

export interface RecordingMessage {
  type: 'recording'
  recording: Recording | null
}

export interface RecordingStoppedPayload {
  recording: RecordingSummary
  dir?: string
  error?: string
}

export interface RecordingAudioPayload {
  audio: AudioClip
  state: RecordingState
}

/** 「清空当前这一步的录音」的广播：removed 是刚被删掉的那几条。 */
export interface RecordingAudioClearedPayload {
  removed: AudioClip[]
  state: RecordingState
}
