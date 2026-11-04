import type { OrderKind, Side } from '@/types/protocol'

/**
 * 挂单类型**由价格相对现价的方向决定**，不由用户手选（右键菜单就是这么用的）。
 *
 * 先把两个定义校准清楚（这是选型的全部依据）：
 *   - **限价单（limit）= 等价格过来**。买挂在现价**下方**（想买得更便宜），
 *     卖挂在现价**上方**（想卖得更贵）。成交价不差于挂单价，代价是可能一直不成交。
 *   - **止损单（stop）= 价格反向确认之后追**。买挂在现价**上方**（突破才追多），
 *     卖挂在现价**下方**（跌破才止损）。触发后是市价，成交价不优于挂单价。
 *
 * 所以同一边、同一个价格，方向不同就该是不同单型：右键点在现价下方，
 * 买入是「等回落」（限价）、卖出是「跌破才走」（止损）；点在现价上方则正好相反。
 * 挂反了的后果不是「不成交」，而是**一挂就成交成市价单**——菜单里必须算死。
 *
 * 相等（价格正好等于收盘价）按限价算：这是唯一不会「立刻变市价」的一侧，
 * 而且后端撮合时限价买 `low <= 价`、限价卖 `high >= 价` 都能正常参与。
 */
export function orderKindFor(side: Side, price: number, close: number): OrderKind {
  // 没有现价（实例还没有 K 线）：给一个保守默认，真正的判定在后端
  if (!(close > 0)) return side === 'buy' ? 'limit' : 'stop'
  if (side === 'buy') return price <= close ? 'limit' : 'stop'
  return price >= close ? 'limit' : 'stop'
}

/** 单型的中文名，菜单文案和后端字段只有这一处映射 */
export function orderKindLabel(kind: OrderKind): string {
  if (kind === 'limit') return '限价'
  if (kind === 'market') return '市价'
  return '止损'
}
