/**
 * 交易页和 Spring 之间的 WebSocket 通道（`/ws`，见后端 `ws/TradeSocketHandler`）。
 *
 * ## 为什么这一页要换成 WebSocket
 *
 * 交易页的动作（【下一根】往后放、以后的画线 / 下单）都会**改盘面**，而且都在后端算：
 * 下一根是哪根要读 CSV，账户的 `lastBarTime` 要落库，操作记录要写 `audit_event`。
 * 一问一答的 HTTP 也能做到，但每加一个动作就要加一个接口、一份 DTO、一套错误处理；
 * 而且「这一步之后盘面变成什么样」还得前端自己拼。
 *
 * 换成一条长连接之后，形状变成：**发一条意图 → 后端算 → 广播结果**。
 * 前端不做任何预演，收到的 `event` 就是唯一事实（连发起方也收，理由见后端 `EventMessage`）。
 *
 * ## 这条通道只管「动作」，不管「取数」
 *
 * 连上之后由前端发 `hello`，后端回一份 `snapshot`（账户 + K 线窗口 + 还有没有下一根）。
 * 之后所有变化都是 `event`。**没有「再取一次数」这条路径** —— 数据只有一个入口，
 * 就不会出现「HTTP 拿到的和 WS 加上去的两段接不上」。
 *
 * ## 断线怎么办
 *
 * 自动重连（1s 起，翻倍，封顶 15s），连上后自动补一条 `hello`，后端会重发 `snapshot`，
 * 整块盘面按后端的样子重建。这里刻意**不在本地缓存动作**排队重发：
 * 一条「步进」在断线期间重发，可能让账户一次往前跳两根，而用户只按了一次。
 */

import type { ClosedLot, TradeOrder, Position, Trade } from '@/types/protocol'

/** 一根 K 线（和后端 `BarView` 同名）。没有 `ema20` —— 均线由前端现算。 */
export interface DeskBar {
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
}

/**
 * 连上之后的第一条回执（后端 `DeskSnapshot`）。
 *
 * **账和 K 线一起给**：断线重连后要按后端的样子重建整块盘面，挂单、成交、持仓
 * 必须和 K 线一起来 —— 分开取会出现「K 线是新的、挂单还是断线前那批」的错位。
 * 几个 `orders` / `trades` / `closedLots` / `position` 和广播里是同一批类型
 * （`types/protocol.ts`），前端只有一套装载代码。
 */
export interface DeskSnapshot {
  type: 'snapshot'
  accountId: string
  accountName?: string
  /** EVAL / FUNDED / PAYOUT */
  stage?: string
  /** ACTIVE / FAILED */
  status?: string
  symbolId: string
  folder: string
  tick?: number
  tickValue?: number
  /** 截止那一天（`yyyy-MM-dd`） */
  day: string
  /** 盘面停在哪一根（`yyyy-MM-dd HH:mm`） */
  barTime: string
  /** 磁盘上还有没有下一根 —— 【下一根】按钮的可用性就看它 */
  hasNext: boolean
  bars: DeskBar[]
  orders: TradeOrder[]
  trades: Trade[]
  closedLots: ClosedLot[]
  position: Position
  /** 净已实现盈亏（已扣手续费） */
  realizedPnl: number
  unrealizedPnl: number
}

/**
 * `bar_advanced` 的负载（后端 `BarAdvancedPayload`，和 `audit_event.payload_json` 同一份）。
 *
 * 撮合就发生在这一步：新揭示的那根 K 线会成交「之前挂的」单，结果全部在这份负载里
 * （`orders` / `trades` / `closedLots` / `position` 是全量，`fills` 是**这一根**触发的）。
 * 所以【下一根】之后不会再补一条 `order_filled`。
 */
export interface BarAdvancedPayload {
  bar: DeskBar
  /** 在这一天的日文件里是第几根（从 0 起）。只用于显示，不是凭证 */
  cursor: number
  /** 这一天还剩几根 */
  hiddenBars: number
  /** 磁盘上还有没有下一根 */
  hasMore: boolean
  orders: TradeOrder[]
  trades: Trade[]
  closedLots: ClosedLot[]
  position: Position
  realizedPnl: number
  unrealizedPnl: number
  /** 只装这一根触发的成交（多数时候为空） */
  fills: Trade[]
}

/**
 * 一条广播（后端 `EventMessage`）：盘面改了。
 *
 * `action` 是小写下划线的**事实名**：`bar_advanced` / `order_placed` / `order_filled` /
 * `order_cancelled` / `order_moved` / `flat_all`。
 */
export interface DeskEvent {
  type: 'event'
  action: string
  payload: unknown
  /** human / bot / system */
  by: string
  /** 账户内序号，断档就说明漏了一条 */
  seq: number
}

export type DeskState = 'connecting' | 'open' | 'closed'

export interface DeskHandlers {
  onSnapshot: (snapshot: DeskSnapshot) => void
  onEvent: (event: DeskEvent) => void
  /** 后端明确拒绝了这个动作（业务原因）。**不是**连接问题 */
  onError: (message: string) => void
  onState: (state: DeskState) => void
}

export interface DeskConnection {
  /** 发一条消息。连接还没就绪时返回 false —— 调用方要给提示，不能假装发出去了。 */
  send: (message: Record<string, unknown>) => boolean
  readonly state: DeskState
  close: () => void
}

/** 心跳间隔：主要是让「半死的连接」（休眠唤醒、代理掐断）能被发现。 */
const PING_MS = 30_000
/** 重连退避：1s 起，每次翻倍，封顶 15s。 */
const RETRY_MIN_MS = 1_000
const RETRY_MAX_MS = 15_000

/**
 * 连上 `/ws` 并进入某个账户。
 *
 * 地址由 `location` 拼，不写死 8791：开发时走 Vite 的 proxy（`/ws` 也代理），
 * 打包后和接口同源。两种情况都不需要再配一遍环境。
 */
export function openDesk(accountId: string, handlers: DeskHandlers): DeskConnection {
  let socket: WebSocket | null = null
  let state: DeskState = 'connecting'
  let retryMs = RETRY_MIN_MS
  let retryTimer = 0
  let pingTimer = 0
  let closedByUser = false

  const setState = (next: DeskState): void => {
    if (state === next) return
    state = next
    handlers.onState(next)
  }

  const stopPing = (): void => {
    if (pingTimer) window.clearInterval(pingTimer)
    pingTimer = 0
  }

  const send = (message: Record<string, unknown>): boolean => {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    socket.send(JSON.stringify(message))
    return true
  }

  const scheduleRetry = (): void => {
    if (closedByUser) return
    if (retryTimer) window.clearTimeout(retryTimer)
    retryTimer = window.setTimeout(connect, retryMs)
    retryMs = Math.min(retryMs * 2, RETRY_MAX_MS)
  }

  const connect = (): void => {
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${scheme}://${location.host}/ws`)
    socket = ws
    setState('connecting')

    ws.onopen = () => {
      retryMs = RETRY_MIN_MS
      setState('open')
      // 每次连上都要重新自报家门：后端不为「连接」保存任何身份
      send({ type: 'hello', accountId })
      stopPing()
      pingTimer = window.setInterval(() => send({ type: 'ping' }), PING_MS)
    }

    ws.onmessage = (raw) => {
      let message: unknown
      try {
        message = JSON.parse(String(raw.data))
      } catch {
        handlers.onError('后端发来了一条不是 JSON 的消息')
        return
      }
      const body = message as { type?: string; msg?: string }
      switch (body.type) {
        case 'snapshot':
          handlers.onSnapshot(message as DeskSnapshot)
          return
        case 'event':
          handlers.onEvent(message as DeskEvent)
          return
        case 'error':
          handlers.onError(body.msg || '后端拒绝了这一步，没给原因')
          return
        case 'pong':
          return
        default:
          handlers.onError(`不认识的下行消息：${body.type ?? '(没有 type)'}`)
      }
    }

    ws.onclose = () => {
      stopPing()
      socket = null
      setState('closed')
      scheduleRetry()
    }

    // onerror 之后浏览器一定会再触发 onclose，重连只在那里做，避免两条线各连一次
    ws.onerror = () => {}
  }

  connect()

  return {
    send,
    get state() {
      return state
    },
    close(): void {
      closedByUser = true
      stopPing()
      if (retryTimer) window.clearTimeout(retryTimer)
      socket?.close()
      socket = null
      setState('closed')
    },
  }
}
