import type { Trade } from '@/types/protocol'

/**
 * 把成交流水还原成「一开一平」的线段，用来在画布上标记已经平掉的仓。
 *
 * 为什么要在前端做这一步：后端只存**每一笔成交**（`Trade`），它不知道哪一笔是开、
 * 哪一笔是平 —— 那是靠「和当前持仓方向相反」推出来的。这里按后端 `applyFill`
 * 同一套**平均成本法**走一遍，就能把「开仓价 → 平仓价」配对出来。
 *
 * 只读、不参与下单，也不改任何状态：**这些标记画在 Canvas 上，不是标注**，
 * 所以天然不能拖动、不能删除（需求第 5 条要求的正是这个）。
 *
 * 三种情况都要照顾到（和后端一致）：
 *   - 同向成交 = 加仓：均价摊平，开仓时间仍是这一仓**第一次**成交的那根；
 *   - 反向成交 = 平仓：能平多少平多少，画一条「开仓均价 → 这笔成交价」的线；
 *   - 反向且量更大 = 反手：平掉全部之后剩下的部分是**新开的仓**，成本 = 本笔成交价。
 */
export interface ClosedTrade {
  /** 开仓那一笔的时间（这一仓第一次成交的 K 线） */
  openTime: string
  /** 开仓均价（加过仓就是摊平后的均价，和后端持仓里的 avg 同一口径） */
  openPrice: number
  closeTime: string
  closePrice: number
  /** 被平掉那一仓的方向：long = 买开的仓，short = 卖开的仓 */
  side: 'long' | 'short'
  /** 这次平掉的数量（部分平仓时小于开仓量） */
  quantity: number
  /** 这笔平仓实现的盈亏（后端算好放在 Trade 里，前端不重算） */
  pnl: number
}

const EPS = 1e-9

export function closedTrades(trades: Trade[]): ClosedTrade[] {
  const out: ClosedTrade[] = []
  let qty = 0
  let avg = 0
  let entryTime = ''

  for (const t of trades) {
    const signed = t.side === 'buy' ? t.quantity : -t.quantity

    if (qty === 0 || qty > 0 === signed > 0) {
      // 空仓开新仓，或同向加仓
      const total = Math.abs(qty) + Math.abs(signed)
      if (total > 0) avg = (Math.abs(qty) * avg + Math.abs(signed) * t.price) / total
      if (qty === 0) entryTime = t.time
      qty += signed
      continue
    }

    // 反向成交：先按能平的数量配一段
    const closeQty = Math.min(Math.abs(qty), Math.abs(signed))
    out.push({
      openTime: entryTime || t.time,
      openPrice: avg,
      closeTime: t.time,
      closePrice: t.price,
      side: qty > 0 ? 'long' : 'short',
      quantity: closeQty,
      pnl: t.realizedPnl,
    })

    const next = qty + signed
    if (Math.abs(next) < EPS) {
      // 全平
      qty = 0
      avg = 0
      entryTime = ''
    } else if (next > 0 !== qty > 0) {
      // 反手：剩下的是这一笔新开的
      qty = next
      avg = t.price
      entryTime = t.time
    } else {
      // 部分平仓：均价不变，剩下的仓位仍从最早那根算起
      qty = next
    }
  }

  return out
}
