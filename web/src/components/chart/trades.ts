import type { Trade } from '@/types/protocol'

/**
 * 把成交流水还原成「一开一平」的线段，用来在画布上标记已经平掉的仓。
 *
 * 为什么要在前端做这一步：后端只存**每一笔成交**（`Trade`），它不知道哪一笔是开、
 * 哪一笔是平 —— 那是靠「和当前持仓方向相反」推出来的。这里按持仓批次重走一遍，
 * 就能把「开仓成交价 → 平仓成交价」配对出来。
 *
 * ⚠️ 这里刻意**不用平均成本法**配对（后端 `applyFill` 仍用它算盈亏，两者不冲突）：
 * 画线是为了让人看清「这笔平仓是从哪个三角开出来的」。用均价配，2 手分别在 100 和 102
 * 开仓、再在 105 一起平掉时会出现两条错：
 *   1. 线的起点在均价 101，不在任何一个开仓三角上（三角在 100 和 102）；
 *   2. 只画出 **1 条**线，看不出是两笔独立的仓位被一起平掉的。
 * 所以这里按 **FIFO 批次** 配对：同一方向的开仓各记一批（时间 + 真实成交价 + 数量），
 * 反向成交从最早的批次开始消耗，消耗几批就画几条线，每条都从那一批的三角出发。
 *
 * 只读、不参与下单，也不改任何状态：**这些标记画在 Canvas 上，不是标注**，
 * 所以天然不能拖动、不能删除。
 */
export interface ClosedTrade {
  /** 这一批开仓成交的 K 线时间（= 画布上那个买/卖三角的横坐标） */
  openTime: string
  /** 这一批开仓的**真实成交价**（三角的纵坐标，不是均价） */
  openPrice: number
  closeTime: string
  closePrice: number
  /** 被平掉那一批的方向：long = 买开的仓，short = 卖开的仓 */
  side: 'long' | 'short'
  /** 这一批被平掉的数量（部分平仓时小于该批的开仓量） */
  quantity: number
  /** 这一笔平仓实现的盈亏里、按数量摊给这一批的那一份（后端算总数，前端只摊） */
  pnl: number
}

/** 一个尚未平掉的开仓批次 */
interface Lot {
  time: string
  price: number
  /** 剩余未平数量（正数） */
  qty: number
}

const EPS = 1e-9

export function closedTrades(trades: Trade[]): ClosedTrade[] {
  const out: ClosedTrade[] = []
  /** 未平仓批次，按成交顺序排队（FIFO） */
  let lots: Lot[] = []
  /** 净头寸（带符号），只用来判断方向翻转 */
  let qty = 0

  for (const t of trades) {
    const signed = t.side === 'buy' ? t.quantity : -t.quantity
    const prevQty = qty

    if (prevQty === 0 || prevQty > 0 === signed > 0) {
      // 空仓开新仓，或同向加仓：各记一批（加仓不摊平已有批次的价格）
      if (prevQty === 0) lots = []
      lots.push({ time: t.time, price: t.price, qty: Math.abs(signed) })
      qty += signed
      continue
    }

    // 反向成交：从最早的批次开始消耗。一笔平仓可能横跨好几批 -> 画好几条线。
    const closeTotal = Math.min(Math.abs(prevQty), Math.abs(signed))
    let left = Math.abs(signed)
    while (left > EPS && lots.length > 0) {
      const lot = lots[0]
      const take = Math.min(lot.qty, left)
      out.push({
        openTime: lot.time,
        openPrice: lot.price,
        closeTime: t.time,
        closePrice: t.price,
        side: prevQty > 0 ? 'long' : 'short',
        quantity: take,
        // 后端给的 realizedPnl 是这一笔的总数，按数量摊到每一批（同一笔平仓里
        // 各批的价格可能不同，摊到的份额只是近似 —— 它只用于展示，不参与结算）
        pnl: closeTotal > EPS ? (t.realizedPnl * take) / closeTotal : 0,
      })
      lot.qty -= take
      left -= take
      if (lot.qty <= EPS) lots.shift()
    }
    qty += signed

    if (Math.abs(qty) < EPS) {
      // 全平
      qty = 0
      lots = []
    } else if (qty > 0 !== prevQty > 0) {
      // 反手：剩下的部分是这一笔新开的仓，成本 = 本笔成交价
      lots = [{ time: t.time, price: t.price, qty: Math.abs(qty) }]
    }
  }

  return out
}
