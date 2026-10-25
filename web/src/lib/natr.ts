/**
 * nATR「以损定仓」：**先定亏多少，再倒推下多少手**。
 *
 * 算式只有三行，但每一行都值得写死在一处：
 *
 *     stopDistance = n × ATR              // 止损距离（点）
 *     riskPerLot   = stopDistance × 每点价值（美元）
 *     lots         = round(maxLoss / riskPerLot)
 *
 * 例：ATR = 19.24、n = 10 → 止损距离 192.4 点；MNQ 每点 $2 → 每手亏 $384.80；
 * 最大亏损 $2000 → 2000 / 384.8 = 5.197 → **5 手**（实际亏 $1924，贴着上限但不超）。
 *
 * **为什么是 round 而不是 floor**：用户定的是「最多亏这么多」，不是「尽量少亏」。
 * 手数越接近 `maxLoss / riskPerLot`，实际亏损就越接近上限 —— 取最接近的那个整数
 * 就是让 |实际亏损 - 最大亏损| 最小的那个。举例（每手 $420）：5 手亏 2100（差 100）、
 * 4 手亏 1800（差 200）、6 手亏 2300（差 300），选 5 手。注意这条规则允许**略超**上限
 * （5 手那 100 美元），这是用户明确定下的口径，别自作聪明改成向下取整。
 *
 * 唯一例外写在 `planNatr` 末尾：`maxLoss` 小到连 1 手都亏不起时，手数仍然是 1
 * （期货没有 0 手，也没有半手），此时 `overRisk = true`，界面要把实际亏损标红 ——
 * 让用户知道「要么别做，要么这个设置根本不合用」，而不是静默给一个看起来正常的数。
 * 取整本身带来的那点超出只算 `over`，界面淡淡提一句就够：那是用户自己定的口径。
 *
 * 每点价值从 `_symbols.json` 的 `tick` / `tickValue` 推出来（`tickValue / tick`），
 * 和后端 `internal/replay/trade.go` 的 `pointValueOf` 同一口径 —— 前端算出来的手数
 * 必须和「成交后后端算的盈亏」对得上，否则用户会看到「说好亏 1924，结果亏了别的」。
 */

/** 用户在【下单】浮窗里填的两个数：几倍 ATR 当止损、最多亏多少美元。 */
export interface NatrSettings {
  /** n：止损距离 = n × ATR（可以带小数，1.5 倍 ATR 是常见用法） */
  n: number
  /** 最大亏损（美元） */
  maxLoss: number
}

/** 出厂值：1 倍 ATR、$200 上限。第一次打开就有个能算出来的数，不用先填表。 */
export const DEFAULT_NATR: NatrSettings = {
  n: 1,
  maxLoss: 200,
}

/** n 的上限没有功能意义，只是挡住手输的 999999（那样的手数一定被夹到 1 手）。 */
export const NATR_N_MAX = 100
/** 最大亏损上限（美元）。写大一点，别挡着做股指期货的人。 */
export const NATR_MAX_LOSS_MAX = 10_000_000
/** 手数上限：和 store 的 `orderQuantity` 同一个数，再大后端也不会接。 */
export const NATR_LOTS_MAX = 10_000

/**
 * 夹到合法区间。和 `sanitizeAtr` / `sanitizeIndicator` 同一个理由：
 * 输入框能被手输成 0、负数、`abc`（NaN），而一个 NaN 会让整块面板静默变成 `—`，
 * 用户只会觉得「这个功能坏了」。所有入口（输入框、读 localStorage）都过这一道。
 */
export function sanitizeNatr(s: NatrSettings): NatrSettings {
  const rawN = Number(s.n)
  const rawLoss = Number(s.maxLoss)
  const n = Number.isFinite(rawN) ? Math.round(rawN * 100) / 100 : DEFAULT_NATR.n
  const maxLoss = Number.isFinite(rawLoss) ? Math.round(rawLoss * 100) / 100 : DEFAULT_NATR.maxLoss
  return {
    n: Math.min(NATR_N_MAX, Math.max(0.1, n)),
    maxLoss: Math.min(NATR_MAX_LOSS_MAX, Math.max(1, maxLoss)),
  }
}

/** 一次计算的完整结果。界面只排版，不再自己乘 —— 两处各算一遍迟早对不上。 */
export interface NatrPlan {
  /** 止损距离（点）= n × ATR */
  stopDistance: number
  /** 每点值多少美元（tickValue / tick；不知道时按 1 兜底） */
  pointValue: number
  /** 每手在 nATR 处的亏损（美元） */
  riskPerLot: number
  /** 理论手数（没取整的那个小数，界面上可以显示出来解释「为什么是 5 不是 6」） */
  exact: number
  /** 最终手数（整数，1..NATR_LOTS_MAX） */
  lots: number
  /** 这么多手亏的钱（= lots × riskPerLot） */
  actualLoss: number
  /** 实际亏损是否超过最大亏损（取整带来的几十美元也算，界面据此提一句，但不报警） */
  over: boolean
  /**
   * **真正的风险**：手数已经压到最小的 1 手，还是亏超了上限。
   *
   * 和 `over` 分开是有必要的：取整几乎总会略微越过上限（2000 的手数算出 4.76 → 取 5 手），
   * 那是用户定的口径、不是错误，每次都标红只会变成噪音。而「1 手都亏不起」是
   * 这个设置根本不合用 —— 没得更小可退，只能别做或改大上限，所以单独一个标志。
   */
  overRisk: boolean
}

/**
 * 算一次。`atr` / `pointValue` 不合法（NaN、≤0）时返回 null ——
 * 界面上就是「算不出手数」，而不是拿一个假数去下单。
 *
 * `atr` 传的是**当前这根 K 线的 ATR 值**（ATR 序列的最后一个有效值），
 * 不是整条序列：这里只关心「现在离场要放多远」。
 */
export function planNatr(atr: number, settings: NatrSettings, pointValue: number): NatrPlan | null {
  if (!Number.isFinite(atr) || atr <= 0) return null
  if (!Number.isFinite(pointValue) || pointValue <= 0) return null

  const { n, maxLoss } = sanitizeNatr(settings)
  const stopDistance = n * atr
  const riskPerLot = stopDistance * pointValue
  if (!(riskPerLot > 0)) return null

  const exact = maxLoss / riskPerLot
  // 最接近上限的那个整数手；夹到 [1, NATR_LOTS_MAX] —— 0 手不是一笔交易
  const lots = Math.min(NATR_LOTS_MAX, Math.max(1, Math.round(exact)))
  const actualLoss = lots * riskPerLot
  // 浮点误差留一点余量：1924.0000000000002 不该被标成「超了」
  const over = actualLoss > maxLoss + 1e-6

  return {
    stopDistance,
    pointValue,
    riskPerLot,
    exact,
    lots,
    actualLoss,
    over,
    // 已经退到 1 手还超 —— 再没有更小的仓位可退，这才是要提醒的那个情况
    overRisk: over && lots <= 1,
  }
}
