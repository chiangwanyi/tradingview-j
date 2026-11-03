/**
 * 机器人的**独立对账器**：只看协议里收到的东西，自己把余额 / 权益 / 持仓 / 亏损线算一遍，
 * 再和后端 `pf` 里那几个数逐个比。
 *
 * ## 为什么值得重算一遍
 *
 * 后端那套口径连着踩过两次坑（2026-11-03 账户「7」与「夜色粽子」），两次都是
 * **测试绿、线上错**：单测直接构造 `Trade` 喂给会话，绕过「订单 → 成交」的接线。
 * 只读接口、拿后端的数当答案，就永远发现不了接线错了。
 *
 * 所以这里刻意**不复用任何后端代码**，只按「协议里能看见的事实」重算：
 *
 * - 余额 = 起点余额 + Σ 成交净额（`Trade.netPnl`，已扣手续费）；
 * - 持仓 = 按平均成本法逐笔重放（多空手数 + 均价）；
 * - 权益 = 余额 + 持仓按**本根最不利价**标的浮盈亏（多单 low / 空单 high）；
 * - 亏损线 = min(日终峰值, 冻结水位) − 最大回撤。
 *
 * ## 「按当根收盘价成交」的成交要退回去
 *
 * 市价单的成交价就是那一根的收盘价，而 low/high 发生在它之前 —— 它没赶上这段波动。
 * 判据不从库里查单型，而是**机器人自己记的**：哪几笔是通过 `market_order` 命令成交的。
 * 这正是对照的意义：服务端按订单表认，机器人按协议事件认，两边一旦不一致就会被打出来。
 */

/** 对账用的成交（字段取自协议 `Trade`，只留算账要用的那些）。 */
export interface OracleTrade {
  id: string
  time: string
  side: 'buy' | 'sell'
  quantity: number
  price: number
  realizedPnl: number
  netPnl: number
}

export interface OracleRules {
  /** 本阶段起点余额 */
  baseBalance: number
  /** 每点价值 = tickValue / tick（后端 `TradeLedger#pointValue` 的同一口径） */
  pointValue: number
  maxDrawdown: number
  /** 冻结水位：峰值到过它之后，亏损线不再上移 */
  lockLevel: number | null
}

export interface OracleBar {
  time: string
  high: number
  low: number
}

export interface OracleReading {
  /** 实际余额（含本根收盘成交那几笔的已实现盈亏），和 `pf.balance` 对齐 */
  balance: number
  /** 按本根最不利价标的权益；日终结算帧除外（那一帧后端给的是 balance） */
  equity: number
  /** 到这一根为止的净持仓（含本根收盘成交的部分），和 `position.quantity` 对齐 */
  quantity: number
  /** 只算「挨过这一根极值」的那部分持仓 —— 权益就是用它的均价标出来的 */
  heldQuantity: number
  /** 逐笔重放出来的均价（只用于对账，不参与任何显示） */
  averagePrice: number
}

/** 金额比较的容差：分位以下不看（服务端到处 `Money.round`，前端是浮点）。 */
export const MONEY_EPS = 0.011

/** 浮点 -> 分，避免 0.1 + 0.2 这类噪声把「余额差 1e-13」报成违例。 */
export const cents = (value: number): number => Math.round(value * 100) / 100

/**
 * 按平均成本法重放持仓。
 *
 * 和 `TradeLedger#replay` 同一套规则：同向加仓按手数加权摊均价、反向先平后开、
 * 反手时剩下的部分成本就是本笔成交价。**这里不做平仓归因**（closedLots），
 * 对账只关心「手上还剩多少、均价多少」。
 */
export function replay(trades: OracleTrade[]): { quantity: number; averagePrice: number } {
  let quantity = 0
  let averagePrice = 0
  for (const trade of trades) {
    const signed = trade.side === 'buy' ? trade.quantity : -trade.quantity
    const prev = quantity
    if (prev === 0 || Math.sign(prev) === Math.sign(signed)) {
      if (prev === 0) averagePrice = 0
      const total = Math.abs(prev) + Math.abs(signed)
      averagePrice = total > 0
        ? (Math.abs(prev) * averagePrice + Math.abs(signed) * trade.price) / total
        : 0
      quantity = prev + signed
      continue
    }
    const next = prev + signed
    if (next === 0) {
      quantity = 0
      averagePrice = 0
    } else if (Math.sign(next) === Math.sign(prev)) {
      quantity = next
    } else {
      quantity = next
      averagePrice = trade.price
    }
  }
  return { quantity, averagePrice }
}

/**
 * 算这一根上的读数。
 *
 * @param trades        该账户到这一根为止的**全部**成交（协议里 `payload.trades` 就是全量）
 * @param closeFilledIds 按当根收盘价成交的那些成交 id（机器人自己记的）
 * @param bar           当前这一根（算极值用）
 */
export function read(
  rules: OracleRules,
  trades: OracleTrade[],
  closeFilledIds: Set<string>,
  bar: OracleBar,
): OracleReading {
  const atClose = trades.filter((t) => t.time === bar.time && closeFilledIds.has(t.id))

  // 余额 = 起点余额 + Σ 成交净额（这是**现在**的余额，和 `pf.balance` 对齐）
  let balance = rules.baseBalance
  for (const trade of trades) balance += trade.netPnl

  // 权益要用「本根收盘成交之前」的账：把那几笔的**已实现盈亏**退回去
  //（手续费不退 —— 那是成交瞬间就付掉的，退回去反而少算了成本）
  let preClose = balance
  for (const trade of atClose) preClose -= trade.realizedPnl

  const beforeClose = atClose.length === 0
    ? trades
    : trades.filter((t) => !(t.time === bar.time && closeFilledIds.has(t.id)))
  const position = replay(beforeClose)

  let equity = preClose
  if (position.quantity !== 0) {
    const worst = position.quantity > 0 ? bar.low : bar.high
    equity += (worst - position.averagePrice) * position.quantity * rules.pointValue
  }
  return {
    balance: cents(balance),
    equity: cents(equity),
    quantity: replay(trades).quantity,
    heldQuantity: position.quantity,
    averagePrice: position.averagePrice,
  }
}

/** 亏损线：EOD 追踪 + 冻结水位。和 `PropFirmSession#floor` 同一个口径。 */
export function floorOf(rules: OracleRules, eodPeak: number): number {
  const peak = rules.lockLevel != null && eodPeak >= rules.lockLevel ? rules.lockLevel : eodPeak
  return cents(peak - rules.maxDrawdown)
}

/**
 * 这一根 K 线属于哪个**交易日**（`[D 06:00, D+1 04:00)`），落在禁交易时段返回 `null`。
 *
 * 和 `PropFirmClock.tradingDayOf` 同一刀：06:00 之前算前一天，而 **04:00–06:00 既不属于
 * 前一天也不属于后一天**（那一段是收尾，日终强平就发生在 04:00 那一根上）。
 * 返回 `null` 不是「算不出来」，是「这一段没有交易日」—— 日终结算的时机判据靠它。
 *
 * 这里只看时间串，不做时区换算 —— 协议里的 `time` 就是交易所本地明文时刻。
 */
export function tradingDayOf(barTime: string): string | null {
  const date = barTime.slice(0, 10)
  const hour = Number(barTime.slice(11, 13))
  if (hour >= 4 && hour < 6) return null
  if (hour >= 6) return date
  const d = new Date(`${date}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() - 1)
  return d.toISOString().slice(0, 10)
}
