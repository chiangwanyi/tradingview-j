/**
 * 机器人专用的 `/ws` 客户端：**和交易页走同一条通道**，只是把「发一条、等回执」包成 Promise。
 *
 * ## 为什么不复用 `data/socket.ts`
 *
 * 那一份是给界面用的：它自己重连、自己把消息分发给 Pinia，动作由用户点击驱动。
 * 机器人要的是**一问一答的顺序性** —— 「下单 → 等 order_filled → 推进一根 → 等 bar_advanced」
 * 必须严格串起来，否则校验会读到上一根的数。这里因此换成 await 风格。
 *
 * 协议完全一样：`hello` / `step` / `market_order` / `order_placed` / `order_cancelled` /
 * `order_moved` / `flat_all`，一个字段都不自创 —— **这正是「和用户同一条下单路径」的含义**：
 * 机器人不调服务层、不直接写库，它就是一个很勤快的浏览器标签页。
 *
 * ## 为什么用全局 WebSocket 而不是 `ws` 包
 *
 * Node 22 自带 `WebSocket`（undici），浏览器也自带。一份代码在页面里和
 * `scripts/bot-run.mts` 里都能跑，不必为「能不能无头运行」多引一个依赖。
 */

/** 一条上行/下行消息。`type` 之外全是协议字段，这里不做全量建模 —— 明细由调用方断言。 */
export type DeskMessage = { type: string; [key: string]: unknown }

/** 单条命令等回执的上限。超过就当这条命令没了（服务端事务慢，或连接半死）。 */
const REPLY_TIMEOUT_MS = 30_000

interface Waiter {
  match: (message: DeskMessage) => boolean
  resolve: (message: DeskMessage) => void
  reject: (error: Error) => void
  timer: ReturnType<typeof setTimeout>
}

export class BotDesk {
  private readonly ws: WebSocket
  private readonly waiters: Waiter[] = []
  private closed = false

  readonly accountId: string
  /** `hello` 的回报（盘面 + 账）。建连之后才有值。 */
  snapshot: DeskMessage | null = null

  private constructor(ws: WebSocket, accountId: string) {
    this.ws = ws
    this.accountId = accountId
    ws.onmessage = (raw) => {
      let message: DeskMessage
      try {
        message = JSON.parse(String(raw.data)) as DeskMessage
      } catch {
        return
      }
      this.dispatch(message)
    }
    ws.onclose = () => {
      this.closed = true
      this.failAll(new Error('连接已断开'))
    }
    ws.onerror = () => {
      /* onerror 之后一定会 onclose，统一在那里收尾 */
    }
  }

  /**
   * 连上并进入账户（发 `hello`、等 `snapshot`）。
   *
   * `hello` 是必须的：后端不认「连接」的身份，没有它后面的命令会广播到一个空房间 ——
   * 库里写进去了、机器人自己什么都收不到。这一点和交易页一模一样。
   */
  static async open(url: string, accountId: string, timeoutMs = 15_000): Promise<BotDesk> {
    const ws = new WebSocket(url)
    const desk = new BotDesk(ws, accountId)

    const opened = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`连不上 ${url}（${timeoutMs}ms 超时）`)), timeoutMs)
      ws.onopen = () => {
        clearTimeout(timer)
        resolve()
      }
    })

    try {
      await opened
      const hello = desk.waitFor((m) => m.type === 'snapshot', timeoutMs)
      ws.send(JSON.stringify({ type: 'hello', accountId }))
      desk.snapshot = await hello
      return desk
    } catch (error) {
      ws.close()
      throw error
    }
  }

  /** 发一条命令并等指定回执。回执没来之前不会发下一条 —— 顺序是校验的前提。 */
  async request(send: DeskMessage, match: (message: DeskMessage) => boolean): Promise<DeskMessage> {
    if (this.closed) throw new Error('连接已断开，命令没有发出去')
    const reply = this.waitFor(match)
    this.ws.send(JSON.stringify({ ...send, accountId: this.accountId }))
    return reply
  }

  /** 【下一根】。回执是 `bar_advanced`，撮合结果就并在这条里。 */
  step(): Promise<DeskMessage> {
    return this.request({ type: 'step' }, (m) => m.type === 'event' && m.action === 'bar_advanced')
  }

  /** 市价单。回执是 `order_filled`（命令名 ≠ 事实名，这是协议里唯一的例外）。 */
  market(side: 'buy' | 'sell', quantity: number, stopDistance?: number): Promise<DeskMessage> {
    const body: DeskMessage = { type: 'market_order', side, quantity }
    if (stopDistance != null) body.stopDistance = stopDistance
    return this.request(body, (m) => m.type === 'event' && m.action === 'order_filled')
  }

  /** 挂限价 / 止损单。 */
  place(side: 'buy' | 'sell', kind: 'limit' | 'stop', price: number, quantity: number): Promise<DeskMessage> {
    return this.request({ type: 'order_placed', side, kind, price, quantity },
      (m) => m.type === 'event' && m.action === 'order_placed')
  }

  cancel(orderId: string): Promise<DeskMessage> {
    return this.request({ type: 'order_cancelled', orderId },
      (m) => m.type === 'event' && m.action === 'order_cancelled')
  }

  move(orderId: string, price: number): Promise<DeskMessage> {
    return this.request({ type: 'order_moved', orderId, price },
      (m) => m.type === 'event' && m.action === 'order_moved')
  }

  flatAll(): Promise<DeskMessage> {
    return this.request({ type: 'flat_all' }, (m) => m.type === 'event' && m.action === 'flat_all')
  }

  close(): void {
    this.closed = true
    this.failAll(new Error('机器人主动断开'))
    this.ws.close()
  }

  // ---------------------------------------------------------------- 内部

  private waitFor(match: (message: DeskMessage) => boolean, timeoutMs = REPLY_TIMEOUT_MS): Promise<DeskMessage> {
    return new Promise((resolve, reject) => {
      const waiter: Waiter = {
        match,
        resolve,
        reject,
        timer: setTimeout(() => {
          this.drop(waiter)
          reject(new Error(`等回执超时（${timeoutMs}ms）：服务端没有回应这条命令`))
        }, timeoutMs),
      }
      this.waiters.push(waiter)
    })
  }

  private drop(waiter: Waiter): void {
    const at = this.waiters.indexOf(waiter)
    if (at >= 0) this.waiters.splice(at, 1)
    clearTimeout(waiter.timer)
  }

  /** 出错时把**所有**等待者都拒掉：一条连接同时只该有一条命令在飞，出错了就没有顺序可言。 */
  private failAll(error: Error): void {
    for (const waiter of this.waiters.splice(0)) {
      clearTimeout(waiter.timer)
      waiter.reject(error)
    }
  }

  private dispatch(message: DeskMessage): void {
    if (message.type === 'error') {
      this.failAll(new Error(String(message.msg ?? '后端拒绝了这一步')))
      return
    }
    for (const waiter of [...this.waiters]) {
      if (waiter.match(message)) {
        this.drop(waiter)
        waiter.resolve(message)
        return
      }
    }
  }
}
