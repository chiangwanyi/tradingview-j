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
  /*
   * 这里**没有** atr：ATR 从 v0.23.0 起由前端现算（Length / Smoothing 是用户调的），
   * 见 `components/chart/atr.ts`。旧的交易记录里还可能带着 `atr14` 这个键 ——
   * 多出来的字段没人读，也就不需要在这里声明。
   */
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
  /**
   * 当前这根 K 线上的语音讲解（v0.16.0）。
   *
   * **永远是对象**（没录过就是空清单），和交易记录**完全无关** —— 语音是盘面数据，
   * 空仓也能录、交易结束后也能录。这是「语音从录制里解耦出来」在协议上的样子。
   */
  voice: VoiceState
  /**
   * 整个实例的语音清单（按 K 线时间排序）。
   *
   * 复盘只能往前走、不能倒回：只有当前那根的清单的话，离开一根就再也听不到了。
   * 这份全量清单只在快照和语音广播时下发，`bar_advanced` 不带（它只换当前那根）。
   */
  voiceAll: AudioClip[]
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
  | TradeRecordedMessage

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
  /** 换根之后的语音状态（清单和「本根 x/9」跟着走，v0.16.0） */
  voice: VoiceState
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

/**
 * 【市价退仓并取消所有挂单】的广播（v0.20.0）。
 *
 * 一次动作可能同时做两件事（撤掉全部挂单 + 市价平掉整个仓位），所以它既不是
 * OrdersPayload 也不是 OrderFilledPayload：`order` / `trade` 只在**真的有仓位**时才有，
 * 空仓只撤单时两者都是 undefined。
 *
 * 盘面一次换完（挂单、成交、持仓、盈亏全给全），前端不需要再要快照。
 */
export interface FlatAllPayload {
  /** 平掉仓位的那张市价单；本来空仓时为 undefined */
  order?: Order
  /** 对应的成交；本来空仓时为 undefined */
  trade?: Trade
  /** 撤销了几张挂单（操作记录里写清「顺手撤了几张」） */
  cancelled: number
  orders: Order[]
  trades: Trade[]
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
  annotationStyled: 'annotation_styled',
  annotationDeleted: 'annotation_deleted',
  annotationCleared: 'annotation_cleared',
  orderPlaced: 'order_placed',
  orderFilled: 'order_filled',
  orderMoved: 'order_moved',
  orderCancelled: 'order_cancelled',
  flatAll: 'flat_all',
  controlChanged: 'control_changed',
  membersChanged: 'members_changed',
  botStarted: 'bot_started',
  botStopped: 'bot_stopped',
  voiceAdded: 'voice_added',
  voiceCleared: 'voice_cleared',
} as const

/**
 * 交易记录里的「语音步」类型名（v0.18.0）。
 *
 * 它**不是一条广播动作**（语音的广播是 voice_added / voice_cleared），只是记录里
 * 那一步的 `kind` —— 这一步不改盘面，只挂一段语音。所以它单独放一个常量，
 * 不和上面的 ACTION 混在一起。
 */
export const STEP_AUDIO = 'recording_audio'

/**
 * 调色板：模板编辑器里的快捷色块。挑的都是「白底上画得出、又不和涨跌红绿撞」的中间调 ——
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

// ---------------- 交易记录（v0.11.0 叫「录像」，v0.18.0 起是自动的交易记录） ----------------

/**
 * 一笔交易的统计，**全部由后端算好**，前端只排版。
 *
 * 为什么不让前端算：这些数字要能经得起追问 —— 口径（尤其 MFE/MAE 只统计
 * 「全程持仓且这一根没有成交」的整根 K 线）必须只有一份实现。前端从记录里的
 * 事件流再算一遍，早晚会和后端的口径对不上。
 *
 * 口径：
 * - `entryPrice` / `entryTime` 这一笔**第一次成交**的价格与 K 线时间
 * - `exitPrice` / `exitTime` 最后一次成交的价格与时间（部分平仓时一路更新）
 * - `quantity` 开仓手数；`maxQuantity` 持仓期间的最大手数（加减仓时两者不同）
 * - `realizedPnl` 全部成交的已实现盈亏之和（美元）
 * - `mfe` / `mae` 最大有利 / 不利波动（美元，`mfe >= 0 >= mae`）
 * - `barsHeld` 持仓期间**完整**走过的 K 线根数
 * - `closed` 是否正常平完（false = 记录结束时仓位还在：实例被删 / 程序退出 / 崩溃草稿）
 */
export interface TradeStats {
  /** long / short（开仓方向） */
  side: 'long' | 'short' | ''
  quantity: number
  maxQuantity: number
  entryTime: string
  entryPrice: number
  exitTime: string
  exitPrice: number
  realizedPnl: number
  mfe: number
  mae: number
  barsHeld: number
  /** 成交笔数（开仓 + 加减仓 + 平仓） */
  fills: number
  /** 这段记录里的操作步数（含起点） */
  steps: number
  closed: boolean
}

/**
 * 用户给一笔交易打的**自定义标签**（v0.21.0）。
 *
 * 名字和颜色都跟着标签本身存（在记录目录的 tags.json 里），所以「下次打标签时
 * 点一下同名标签」就还原成一模一样的样子 —— 颜色不是主题色查出来的。
 */
export interface RecordTag {
  name: string
  /** `#rrggbb`。后端会把写错的颜色落回中性灰，前端不重复校验。 */
  color: string
}

/**
 * 一笔交易的**操作来源**：human = 真人手动，bot = 机器人。
 *
 * 机器人再细分一层看 `sourceId`（`demo` = 随机演示，其余是策略 id）——
 * 「哪个策略」属于筛选维度，「是不是机器人」属于记录的性质，两者分开存。
 */
export type RecordSource = 'human' | 'bot'

/** 交易记录列表里的一行。 */
export interface RecordingSummary {
  id: string
  /** 一眼能认出是哪一笔：标的 · 方向手数 · 盈亏 · 进出时间 */
  name: string
  symbol: string
  folder: string
  /** 第 1 步（开仓**之前**那一屏）的 K 线时间 */
  anchor: string
  endTime: string
  /**
   * 这笔交易发生在哪个实例上。**只用于追溯和显示**：记录是独立的一份数据，
   * 实例被删掉之后这里会指向一个不存在的 id，打开 / 回放 / 删除都不依赖它。
   */
  instanceId?: string
  /**
   * 摘要里的计数（带 Count 是为了不和 Recording.steps 那个数组撞名）。
   *
   * `stepCount` 是**操作步数**（一步一个操作，所以比根数大得多），
   * 想知道这笔走了多长看 `bars`（推进了几根 K 线）。
   */
  stepCount: number
  audioCount: number
  eventCount: number
  bars: number
  size: number
  createdBy: string
  createdAt: number
  /** 收尾时间；还在持仓中时等于最后一次写盘时间 */
  endedAt: number
  stats: TradeStats
  /**
   * 操作来源（v0.21.0）。旧记录由后端按 `createdBy` 反推后一起下发，
   * 所以这里可以当作**一定存在**来读（读不到时前端还有一层兜底，见 lib/records.ts）。
   */
  source?: RecordSource
  /** 机器人的策略 id（`demo` = 随机演示）；真人记录没有这个字段 */
  sourceId?: string
  /** 给人看的细分名：真人是操作者名字，机器人是策略名（随机演示 / Caveman（野人）） */
  sourceName?: string
  /** 用户打的自定义标签，顺序即打标签的顺序 */
  tags?: RecordTag[]
}

/** 开仓**之前**那一刻的盘面。 */
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
 * 交易记录里的一步 —— **一步 = 一次真实操作**（v0.15.0）。
 *
 * 第 1 步是起点（`kind: 'base'`，没有事件，它就是「开仓之前那一屏」）；之后
 * 每一条放行的广播开一步：点一次【下一根】、画一个图形、拖一次挂单价、说一段话，
 * 各占一步。第 2 步必然是把仓位从 0 变成非 0 的那一笔成交 —— 入境那一下看得见。
 *
 * 语音步（`kind: STEP_AUDIO`）特殊：`events` 为空（不改盘面），
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
  /** 仅语音步：挂在这一步上的那一段 */
  audio?: AudioClip
  events: RecordingEvent[]
}

/** 一段挂在某根 K 线上的录音。`time` 是 K 线时间（存储键）。 */
export interface AudioClip {
  id: string
  /** 在交易记录里是第几步；0 = 空仓时录的（语音是盘面数据，和记录无关） */
  step: number
  time: string
  durationMs: number
  /** 文件名（记录里是 audio-N.wav，实例上的语音是 voice-N.wav），音频本身走 HTTP 取 */
  file: string
  size: number
  createdAt: number
  /** 谁录的（human / bot）；旧记录里没有这个字段 */
  by?: string
}

/**
 * 当前这根 K 线上的语音讲解（v0.16.0）。
 *
 * 服务端数的，界面只显示：界面写「9/9」而磁盘上只有 8 条这种事，
 * 一旦出现就查不清是谁算错了。
 */
export interface VoiceState {
  /** 这份清单属于哪根 K 线（换根就整体换一份） */
  barTime: string
  /** 这根 K 线上的语音（按录制顺序，含更早几步里录的） */
  clips: AudioClip[]
  /** 整个实例一共几条（**不限**，只有同一根 K 线才限 9 条） */
  total: number
  /** 同一根 K 线的上限（9） */
  max: number
}

/** 一条完整的交易记录：开仓前的盘面 + 每一步的事件 + 语音。 */
export interface Recording extends RecordingSummary {
  /**
   * 记录格式：2 = 一步一个操作（v0.15.0 起），1 = 旧的一步一根 K 线。
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

/**
 * 「一笔交易刚刚收尾、记录已保存」的一次性通知（v0.18.0）。
 *
 * 它**不是广播**（不进实例的操作记录）：交易记录是独立的数据集，和事件流不是一回事。
 * 前端拿它做两件事 —— 刷新列表、给一句「这笔赚了多少」的轻提示；
 * 不需要也不应该因此把界面切到某种「正在记录」的状态（那个状态已经被取消了）。
 *
 * `warning` 非空表示记录本身是完整的，只是没落到磁盘（没配目录 / 磁盘满）。
 */
export interface TradeRecordedMessage {
  type: 'trade_recorded'
  recording: RecordingSummary
  dir?: string
  warning?: string
}

/** 「盘面上多了一段语音」的广播：清单与计数都是服务端算好的。 */
export interface VoiceAddedPayload {
  audio: AudioClip
  voice: VoiceState
  /** 整个实例的清单（「其他 K 线还有 N 条」要用） */
  voiceAll: AudioClip[]
}

/** 「这根 K 线上的语音全被清掉」的广播。 */
export interface VoiceClearedPayload {
  removed: AudioClip[]
  voice: VoiceState
  voiceAll: AudioClip[]
}
