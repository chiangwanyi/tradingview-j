/**
 * 交易机器人：建一批 EVAL 账户，用**和用户完全一样的下单路径**跑盘，边跑边对账。
 *
 * ## 它检验什么
 *
 * 单测直接构造 `Trade` 喂给会话，绕过「订单 → 成交 → 重放」这条接线 ——
 * 2026-11-03 的两次误判（账户「7」与「夜色粽子」）都是接线错了而单测全绿。
 * 这个机器人补的就是那段：
 *
 * 1. **HTTP 建户**（`POST /api/pf-accounts`），拿到真实账户和规则快照；
 * 2. **`/ws` 下单**：`market_order` / `order_placed` / `order_cancelled` / `order_moved` /
 *    `flat_all`，一条都不绕过后端服务层；
 * 3. **`step` 推进**，撮合、盈亏、撞线、日终结算全在服务端发生；
 * 4. 每根 K 线回来，用 {@link read} **独立重算**余额 / 权益 / 持仓 / 亏损线，和 `pf` 对账。
 *
 * ## 它不做什么
 *
 * 不判定「策略好坏」，也不追求盈利 —— 随机下单只是为了把各种边界撞出来：
 * 同根平仓再接同向仓、限价单成交、止损单成交、穿线强平、日终强平、退仓撤单……
 * 跑完留下的违例清单才是产出。
 */

import { BotDesk, type DeskMessage } from './desk.ts'
import {
  floorOf, MONEY_EPS, read, tradingDayOf,
  type OracleBar, type OracleRules, type OracleTrade,
} from './oracle.ts'

/** 建户请求的返回（后端 `Account`，只取机器人要用的字段）。 */
interface CreatedAccount {
  id: string
  name: string
  stage: string
  status: string
  symbolId: string
  baseBalance: number
  lastBarTime: string
  rules: {
    accountSize: number | null
    maxDrawdown: number | null
    trailingLockLevel: number | null
    maxContracts?: number | null
    positionTiers?: { fromProfit: number; maxContracts: number }[]
  }
}

export interface BotConfig {
  /** 后端根地址，空串 = 同源（页面里跑就用空串，靠 vite 代理） */
  baseUrl: string
  /** `/ws` 的完整地址；不给就按 baseUrl 推 */
  wsUrl?: string
  /** EVAL 模板 id（机器人只开 EVAL —— 考核阶段的判定逻辑最全） */
  templateId: string
  /** 交易标的，如 `CME_MINI:MES1!` */
  symbolId: string
  /** 开几个账户 */
  accounts: number
  /** 每个账户最多推进多少根 K 线 */
  maxBars: number
  /** 随机种子：同一个种子跑出同一串动作，违例可复现 */
  seed: number
  /** 同时在跑的账户数（服务端 SQLite 只有一条连接，开太大只会排队） */
  concurrency: number
  /** 账户名前缀，方便跑完一把清理 */
  namePrefix: string
}

export type ViolationKind =
  | 'SEQ_GAP'
  | 'BALANCE_DRIFT'
  | 'POSITION_DRIFT'
  | 'EQUITY_DRIFT'
  | 'FLOOR_DRIFT'
  | 'REALIZED_DRIFT'
  | 'MISSED_FAIL'
  | 'FAIL_NOT_FLAT'
  | 'ORDERS_NOT_CANCELLED'
  | 'FILL_OUT_OF_RANGE'
  | 'TRADE_DUPLICATED'
  | 'SNAPSHOT_MISMATCH'

export interface BotViolation {
  kind: ViolationKind
  account: string
  barTime: string
  detail: string
}

export type RunStatus = 'PENDING' | 'RUNNING' | 'DONE' | 'FAILED_ACCOUNT' | 'FROZEN' | 'STOPPED' | 'ERROR'

export interface AccountRun {
  id: string
  name: string
  status: RunStatus
  bars: number
  orders: number
  trades: number
  balance: number
  equity: number
  lossLine: number
  /** 撞线那一刻：哪根、权益多少 */
  failBar?: string
  failEquity?: number
  violations: BotViolation[]
  error?: string
}

export interface BotState {
  running: boolean
  done: boolean
  runs: AccountRun[]
  totalBars: number
  totalTrades: number
  totalOrders: number
  violations: BotViolation[]
  startedAt: string
  finishedAt?: string
}

/** 平均成本法算账时用的最小读数（`payload.pf`），只挑机器人要的那几个字段。 */
interface PfView {
  status: 'ACTIVE' | 'FAILED'
  balance: number
  eodPeakBalance: number
  lossLine: number
  equity: number
  frozen: boolean
  hint?: string
}

interface Payload {
  bar?: { time: string; open: number; high: number; low: number; close: number }
  orders?: { id: string; side: 'buy' | 'sell'; kind: string; price: number; quantity: number; status: string }[]
  trades?: OracleTrade[]
  position?: { quantity: number; side: string; averagePrice: number }
  realizedPnl?: number
  pf?: PfView
  fills?: OracleTrade[]
  trade?: OracleTrade
}

/** mulberry32：小、快、同种子同序列 —— 违例要能复现，所以不能用 `Math.random`。 */
function rngOf(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/** 金额比较：差过一分就算漂。 */
const drift = (a: number, b: number): boolean => Math.abs(a - b) > MONEY_EPS

/**
 * 五个角色的动作脚本。**角色只是概率不同**，不是五套代码 ——
 * 目的是让随机动作覆盖到全部边界，而不是模拟某个策略的收益曲线。
 */
const ARCHETYPES = ['churn', 'scalp', 'maker', 'breakout', 'holder', 'blowup'] as const
type Archetype = (typeof ARCHETYPES)[number]

export class BotRun {
  readonly cfg: BotConfig
  readonly state: BotState
  private stopped = false

  constructor(cfg: BotConfig) {
    this.cfg = cfg
    this.state = {
      running: false,
      done: false,
      runs: [],
      totalBars: 0,
      totalTrades: 0,
      totalOrders: 0,
      violations: [],
      startedAt: new Date().toISOString(),
    }
  }

  stop(): void {
    this.stopped = true
  }

  /** 跑完全部账户。中途 `stop()` 会让正在跑的那一轮在当前这根之后收手。 */
  async run(): Promise<BotState> {
    this.state.running = true
    const cfg = this.cfg
    const created: CreatedAccount[] = []
    for (let i = 0; i < cfg.accounts; i++) {
      if (this.stopped) break
      created.push(await this.createAccount(i))
    }

    this.state.runs = created.map((account) => ({
      id: account.id,
      name: account.name,
      status: 'PENDING' as RunStatus,
      bars: 0,
      orders: 0,
      trades: 0,
      balance: account.baseBalance,
      equity: account.baseBalance,
      lossLine: account.baseBalance - (account.rules.maxDrawdown ?? 0),
      violations: [],
    }))

    // 并发池：SQLite 那边只有一条连接，池子再大也是排队 —— 这里开 3 个只是为了让
    // 「一个账户在等回执」的空档被别的账户用上，而不是真的并行写库。
    const queue = created.map((account, index) => ({ account, index }))
    const workers = Array.from({ length: Math.max(1, Math.min(cfg.concurrency, queue.length)) }, () => this.worker(queue))
    await Promise.all(workers)

    this.state.running = false
    this.state.done = true
    this.state.totalOrders = this.state.runs.reduce((sum, run) => sum + run.orders, 0)
    this.state.totalTrades = this.state.runs.reduce((sum, run) => sum + run.trades, 0)
    this.state.finishedAt = new Date().toISOString()
    return this.state
  }

  private async worker(queue: { account: CreatedAccount; index: number }[]): Promise<void> {
    for (;;) {
      const item = queue.shift()
      if (!item || this.stopped) return
      const run = this.state.runs[item.index]
      try {
        await this.drive(item.account, item.index, run)
      } catch (error) {
        run.status = 'ERROR'
        run.error = error instanceof Error ? error.message : String(error)
      }
    }
  }

  private async createAccount(index: number): Promise<CreatedAccount> {
    const name = `${this.cfg.namePrefix}${String(this.cfg.seed).slice(-4)}-${index + 1}`
    const response = await fetch(`${this.cfg.baseUrl}/api/pf-accounts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ templateId: this.cfg.templateId, symbolId: this.cfg.symbolId, name }),
    })
    const body = (await response.json()) as { code: number; msg: string; data: CreatedAccount }
    if (body.code !== 0) throw new Error(`建户失败（${name}）：${body.msg}`)
    return body.data
  }

  /** 一个账户的一生：开仓、推进、对账，直到撞线 / 冻结 / 步数用完。 */
  private async drive(account: CreatedAccount, index: number, run: AccountRun): Promise<void> {
    const cfg = this.cfg
    const wsUrl = cfg.wsUrl ?? `${cfg.baseUrl.replace(/^http/, 'ws')}/ws`
    const desk = await BotDesk.open(wsUrl, account.id)
    const rng = rngOf(cfg.seed + index * 7919)

    const pointValue = pointValueOf(desk.snapshot)
    const tick = tickOf(desk.snapshot)
    const rules: OracleRules = {
      baseBalance: account.baseBalance,
      pointValue,
      maxDrawdown: account.rules.maxDrawdown ?? 0,
      lockLevel: account.rules.trailingLockLevel ?? null,
    }
    // 手数上限取 EVAL 的第一档（fromProfit 0），机器人不追利润，只用最小档
    const maxContracts = account.rules.positionTiers?.[0]?.maxContracts ?? 4

    const closeFilledIds = new Set<string>()
    const seenTradeIds = new Set<string>()
    const archetype: Archetype = ARCHETYPES[index % ARCHETYPES.length]

    const ctx: AuditCtx = { rules, closeFilledIds, peak: account.baseBalance, prev: null }
    let lastBar: OracleBar | null = null
    let lastSeq: number | null = null
    let trades: OracleTrade[] = []
    let orders: { id: string; side: 'buy' | 'sell'; kind: string; price: number; quantity: number; status: string }[] = []
    let position = { quantity: 0, side: 'flat', averagePrice: 0 }
    let close = 0
    let barTime = account.lastBarTime
    let pf = (desk.snapshot?.pf ?? null) as PfView | null

    try {
      for (let step = 0; step < cfg.maxBars; step++) {
        if (this.stopped) {
          run.status = 'STOPPED'
          return
        }

        // ---- 1) 下单：动作全部走协议，和人在页面上点是同一条路 ----
        // 04:00–06:00 是禁交易时段（两端都关），这时只能平仓和撤单。这里直接不下新单 ——
        // 硬发会被后端拒（那是**规则在生效**，不是缺陷），但机器人不该把合法拒绝当发现。
        const hour = Number(barTime.slice(11, 13))
        const gap = hour >= 4 && hour < 6
        const action = gap ? [] : this.decide(rng, archetype, { position, orders, close, tick, maxContracts })
        for (const act of action) {
          try {
            // 每条命令的广播都带 seq —— 记账流的序号是「这一账户的第几条事实」，
            // 下单也占号，所以等下单回执时就要跟着走，不能只盯 bar_advanced
            let reply: DeskMessage
            if (act.type === 'market') {
              reply = await desk.market(act.side, act.quantity, act.stopDistance)
            } else if (act.type === 'place') {
              reply = await desk.place(act.side, act.kind, act.price, act.quantity)
            } else if (act.type === 'cancel') {
              reply = await desk.cancel(act.orderId)
            } else if (act.type === 'move') {
              reply = await desk.move(act.orderId, act.price)
            } else {
              reply = await desk.flatAll()
            }
            run.orders += 1
            this.noteTrade(run, reply, ctx, seenTradeIds, barTime)

            /*
             * ⚠️ 这里是**最要紧的一次对账**：市价单成交在「已经揭示的那一根」上，
             * 它的权益读数只出现在这条回执里（下一根 bar_advanced 里那笔成交已成历史）。
             * 2026-11-03 账户「夜色粽子」被误判失败，错就错在这一帧的权益上（47951 而不是 48096）——
             * 只查 bar_advanced 的机器人**永远看不到它**。
             */
            lastSeq = this.noteSeq(run, reply, barTime, lastSeq)
            if (lastBar) this.audit(run, ctx, reply.payload as Payload, lastBar, 'order')
          } catch (error) {
            // 「已经失败了」「没有仓位」「禁交易时段」这类业务拒绝不算违例：机器人就是要撞这些边界
            const message = error instanceof Error ? error.message : String(error)
            if (/失败|没有仓位|没有.*挂单|不用退仓|已撤销|已成交|禁止交易时段|只能平仓/.test(message)) break
            throw error
          }
        }

        // ---- 2) 推进一根：撮合、判线、日终结算都在这一步发生 ----
        const event = await desk.step()
        const payload = event.payload as Payload
        if (!payload.bar || !payload.pf) continue

        run.bars += 1
        this.state.totalBars += 1
        barTime = payload.bar.time
        close = payload.bar.close
        lastBar = payload.bar
        trades = payload.trades ?? []
        orders = (payload.orders ?? []).filter((o) => o.status === 'open')
        position = payload.position ?? position
        pf = payload.pf

        // 撮合成交（挂单被扫）在这一条里，价格必须落在这一根的区间里
        for (const fill of payload.fills ?? []) {
          const outOfRange = fill.price < payload.bar.low - 1e-9 || fill.price > payload.bar.high + 1e-9
          if (outOfRange) {
            this.violate(run, 'FILL_OUT_OF_RANGE', barTime,
              `成交 ${fill.side} ${fill.quantity} @ ${fill.price} 不在这根 [${payload.bar.low}, ${payload.bar.high}] 里`)
          }
        }

        // ---- 3) 对账 ----
        lastSeq = this.noteSeq(run, event, barTime, lastSeq)
        this.audit(run, ctx, payload, payload.bar, 'bar')

        if (pf.status === 'FAILED') {
          if (position.quantity !== 0) {
            this.violate(run, 'FAIL_NOT_FLAT', barTime, `已判失败，持仓却还有 ${position.quantity} 手`)
          }
          if (orders.length > 0) {
            this.violate(run, 'ORDERS_NOT_CANCELLED', barTime, `已判失败，却还挂着 ${orders.length} 张单`)
          }
        }

        run.balance = pf.balance
        run.equity = pf.equity
        run.lossLine = pf.lossLine
        run.trades = trades.length

        if (pf.status === 'FAILED') {
          run.status = 'FAILED_ACCOUNT'
          run.failBar = barTime
          run.failEquity = pf.equity
          return
        }
        if (pf.frozen) {
          run.status = 'FROZEN'
          return
        }
        run.status = 'RUNNING'
      }
      run.status = 'DONE'
    } finally {
      desk.close()
    }
  }

  /** 这一根要不要动手、动什么手。返回的动作按顺序执行，空数组 = 袖手旁观。 */
  private decide(
    rng: () => number,
    archetype: Archetype,
    ctx: {
      position: { quantity: number; side: string }
      orders: { id: string; side: 'buy' | 'sell'; kind: string; price: number; quantity: number; status: string }[]
      close: number
      tick: number
      maxContracts: number
    },
  ): Action[] {
    const { position, orders, close, tick } = ctx
    if (close <= 0) return []
    const qty = 1 + Math.floor(rng() * Math.min(3, ctx.maxContracts))
    const ask = (n: number) => Math.round((close + n) / tick) * tick
    const openOrder = orders.find((o) => o.status === 'open')

    // 每个角色一条主动作概率表；随机数仍然全局共用，种子变了动作就全变
    const flat = position.quantity === 0
    switch (archetype) {
      case 'churn': {
        // 同根「平掉再同向重开」—— 两次误判都出在这个场景，必须反复撞
        if (!flat && rng() < 0.55) {
          const side = position.quantity > 0 ? 'sell' : 'buy'
          return [
            { type: 'market', side, quantity: Math.abs(position.quantity) },
            { type: 'market', side, quantity: qty },
          ]
        }
        if (flat && rng() < 0.6) return [{ type: 'market', side: rng() < 0.5 ? 'buy' : 'sell', quantity: qty, stopDistance: tick * 120 }]
        return []
      }
      case 'scalp': {
        if (!flat && rng() < 0.5) {
          return [{ type: 'market', side: position.quantity > 0 ? 'sell' : 'buy', quantity: Math.abs(position.quantity) }]
        }
        if (flat && rng() < 0.7) return [{ type: 'market', side: rng() < 0.5 ? 'buy' : 'sell', quantity: qty, stopDistance: tick * 160 }]
        return []
      }
      case 'maker': {
        if (openOrder && rng() < 0.3) return [{ type: 'cancel', orderId: openOrder.id }]
        if (openOrder && rng() < 0.3) return [{ type: 'move', orderId: openOrder.id, price: ask((rng() - 0.5) * tick * 40) }]
        if (rng() < 0.5 && orders.length < 2) {
          const buy = rng() < 0.5
          return [{ type: 'place', side: buy ? 'buy' : 'sell', kind: 'limit', price: buy ? ask(-tick * 2) : ask(tick * 2), quantity: qty }]
        }
        return []
      }
      case 'breakout': {
        if (rng() < 0.35 && orders.length < 2) {
          const buy = rng() < 0.5
          return [{ type: 'place', side: buy ? 'buy' : 'sell', kind: 'stop', price: buy ? ask(tick * 3) : ask(-tick * 3), quantity: qty }]
        }
        if (!flat && rng() < 0.2) return [{ type: 'flat' }]
        return []
      }
      case 'blowup': {
        // 一次打满手数、拿到底：专门把「撞线 → 强平 → FAILED」这条终态路走一遍。
        // 40 手 MES 是 200 美元/点，回撤 2000 只要走 10 个点 —— 基本一两根就完事。
        if (flat && rng() < 0.9) {
          return [{ type: 'market', side: rng() < 0.5 ? 'buy' : 'sell', quantity: ctx.maxContracts }]
        }
        return []
      }
      default: {
        // holder：开一次就拿到底，专门测撞线强平与日终强平
        if (flat && rng() < 0.5) {
          return [{ type: 'market', side: rng() < 0.5 ? 'buy' : 'sell', quantity: qty }]
        }
        return []
      }
    }
  }

  /**
   * 记一笔「刚落下的成交」，并把它标成**按当根收盘价成交**。
   *
   * `market_order` 和 `flat_all` 的平仓腿都是市价单（成交价 = 当根收盘价），
   * 对账时不能拿这一根的极值去标它们。
   */
  private noteTrade(run: AccountRun, reply: DeskMessage, ctx: AuditCtx,
                    seen: Set<string>, barTime: string): void {
    const payload = reply.payload as Payload
    const trade = payload?.trade
    if (!trade) return
    ctx.closeFilledIds.add(trade.id)
    if (seen.has(trade.id)) {
      this.violate(run, 'TRADE_DUPLICATED', barTime, `成交 ${trade.id} 出现了两次`)
    }
    seen.add(trade.id)
  }

  /**
   * 对账：拿协议里的全量成交把账重算一遍，和后端给的读数逐项比。
   *
   * @param source `bar` = 推进一根的回执；`order` = 下单成交的回执
   */
  private audit(run: AccountRun, ctx: AuditCtx, payload: Payload, bar: OracleBar,
                source: 'bar' | 'order'): void {
    const pf = payload.pf
    const trades = payload.trades
    if (!pf || !trades) return
    const time = bar.time
    const reading = read(ctx.rules, trades, ctx.closeFilledIds, bar)

    if (drift(reading.balance, pf.balance)) {
      this.violate(run, 'BALANCE_DRIFT', time, `后端余额 ${pf.balance}，重算 ${reading.balance}`)
    }
    const position = payload.position
    if (position && reading.quantity !== position.quantity) {
      this.violate(run, 'POSITION_DRIFT', time, `后端持仓 ${position.quantity}，重算 ${reading.quantity}`)
    }
    const realized = centsOf(trades.reduce((sum, t) => sum + t.netPnl, 0))
    if (payload.realizedPnl != null && drift(realized, payload.realizedPnl)) {
      this.violate(run, 'REALIZED_DRIFT', time, `已实现 ${payload.realizedPnl}，成交合计 ${realized}`)
    }

    /*
     * 日终那一根发布的是**结算读数**（`equity` 直接等于 `balance`），不是按极值标出来的，
     * 所以那种帧不参与「权益」比对 —— 它和「浮亏多少」不是同一件事。
     */
    const settled = pf.equity === pf.balance && reading.heldQuantity !== 0
    if (pf.status === 'ACTIVE' && !settled && drift(reading.equity, pf.equity)) {
      this.violate(run, 'EQUITY_DRIFT', time, `后端权益 ${pf.equity}，重算 ${reading.equity}`)
    }

    if (source === 'order') {
      // 下单回执只对金额与权益：撞线要等【下一根】才结算（这是当前实现的时序，见 TradeDeskService#step）
      const expected = floorOf(ctx.rules, ctx.peak)
      if (drift(expected, pf.lossLine)) {
        this.violate(run, 'FLOOR_DRIFT', time, `后端亏损线 ${pf.lossLine}，重算 ${expected}（峰值 ${ctx.peak}）`)
      }
      return
    }

    if (pf.status === 'ACTIVE' && !settled && reading.equity < pf.lossLine - MONEY_EPS) {
      this.violate(run, 'MISSED_FAIL', time,
        `权益 ${reading.equity} 已在亏损线 ${pf.lossLine} 之下，状态却还是 ACTIVE`)
    }

    /*
     * 亏损线**晚一根再查**：哪一根是这一天最后一根，要等下一根才看得出来。
     * 后端在 `feed()` 收尾时补一次日终结算（`settleIfDayOver`），新峰值算出的线直接发在当根上，
     * 所以顺序是：判断上一根是不是日终 → 它的收盘余额进峰值 → 用新峰值去比**上一根**的线。
     * 04:00 那一根会被判成日终两次（收尾第一根 + 强平之后余额又变），峰值取大的那个 ——
     * 和后端 `settleDay` 每次重放都看一遍余额是同一个行为。
     */
    const day = tradingDayOf(time)
    if (ctx.prev) {
      if ((day ?? null) !== (ctx.prev.day ?? null)) {
        ctx.peak = Math.max(ctx.peak, ctx.prev.balance)
      }
      const expected = floorOf(ctx.rules, ctx.peak)
      if (drift(expected, ctx.prev.lossLine)) {
        this.violate(run, 'FLOOR_DRIFT', ctx.prev.time,
          `后端亏损线 ${ctx.prev.lossLine}，重算 ${expected}（峰值 ${ctx.peak}）`)
      }
    }
    ctx.prev = { time, day, balance: pf.balance, lossLine: pf.lossLine }
  }

  /**
   * 记一条事实的序号，顺手查断档。
   *
   * `seq` 是**账户内事实流水**的序号，下单、成交、推进各占一号 —— 只盯 `bar_advanced`
   * 一定会看到「一次跳两三号」的假断档（那正是机器人第一版报出的 14 条假违例）。
   */
  private noteSeq(run: AccountRun, event: DeskMessage, barTime: string, last: number | null): number | null {
    if (event.type !== 'event') return last
    const seq = event.seq
    if (typeof seq !== 'number') return last
    if (last != null && seq !== last + 1) {
      this.violate(run, 'SEQ_GAP', barTime, `seq ${last} → ${seq}，中间少了 ${seq - last - 1} 条`)
    }
    return seq
  }

  private violate(run: AccountRun, kind: ViolationKind, barTime: string, detail: string): void {
    if (run.violations.length >= 50) return
    const violation: BotViolation = { kind, account: run.name, barTime, detail }
    run.violations.push(violation)
    this.state.violations.push(violation)
  }
}

interface AuditCtx {
  rules: OracleRules
  closeFilledIds: Set<string>
  /** 本阶段日终余额峰值（只增不减） */
  peak: number
  /** 上一根的读数，亏损线晚一根再查要用 */
  prev: { time: string; day: string | null; balance: number; lossLine: number } | null
}

type Action =
  | { type: 'market'; side: 'buy' | 'sell'; quantity: number; stopDistance?: number }
  | { type: 'place'; side: 'buy' | 'sell'; kind: 'limit' | 'stop'; price: number; quantity: number }
  | { type: 'cancel'; orderId: string }
  | { type: 'move'; orderId: string; price: number }
  | { type: 'flat' }

const centsOf = (value: number): number => Math.round(value * 100) / 100

function tickOf(snapshot: DeskMessage | null): number {
  const tick = snapshot?.tick
  return typeof tick === 'number' && tick > 0 ? tick : 0.25
}

/** 每点价值 = tickValue / tick；缺配置时和后端一样退化成「一个点 1 美元」。 */
function pointValueOf(snapshot: DeskMessage | null): number {
  const tick = snapshot?.tick
  const tickValue = snapshot?.tickValue
  if (typeof tick === 'number' && typeof tickValue === 'number' && tick > 0 && tickValue > 0) {
    return tickValue / tick
  }
  return 1
}
