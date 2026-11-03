/**
 * 机器人对账器的自检：`npm run check` 会跑它。
 *
 * 对账器要是自己算错了，压测通过就毫无意义（它会把后端的错当成对的）。
 * 所以这里把三个「已知正确」的场景钉死 —— 它们和后端
 * `PropFirmSessionTest` 里那三条是同一组数：
 *
 * 1. 同根「平掉再同向重开」不能把同一段亏损记两次；
 * 2. 收盘才成交的市价单不承担同根极值；
 * 3. 整段仓位在本根收盘平掉，仍要挨这一根的极值。
 *
 * 每点价值统一按 5 美元（MES：tickValue 1.25 / tick 0.25）。
 */
import assert from 'node:assert/strict'

import { floorOf, read, replay, tradingDayOf } from '../src/lib/bot/oracle.ts'
import type { OracleRules, OracleTrade } from '../src/lib/bot/oracle.ts'

const rules: OracleRules = { baseBalance: 50000, pointValue: 5, maxDrawdown: 2000, lockLevel: 52100 }

let seq = 0
const fill = (
  time: string,
  side: 'buy' | 'sell',
  price: number,
  quantity: number,
  realizedPnl = 0,
  commission = 0,
): OracleTrade => ({
  id: `t${++seq}`,
  time,
  side,
  price,
  quantity,
  realizedPnl,
  netPnl: Math.round((realizedPnl - commission) * 100) / 100,
})

// ---- 1) 同根平掉再同向重开 ----
{
  const trades = [
    fill('2026-05-26 16:00', 'sell', 30347, 5),
    fill('2026-05-26 16:40', 'buy', 30388, 5, -410),
    fill('2026-05-26 16:40', 'sell', 30388, 5),
  ]
  const reading = read(rules, trades, new Set([trades[1].id, trades[2].id]),
    { time: '2026-05-26 16:40', high: 30392.25, low: 30376.75 })
  assert.equal(reading.balance, 49590, '余额 = 50000 − 410')
  assert.equal(reading.equity, 48868.75, '权益 = 50000 − 45.25×5×5，不能把 −410 记两遍')
}

// ---- 2) 收盘才成交的市价单不吃同根极值 ----
{
  const held = fill('2024-03-04 09:25', 'buy', 7775.5, 15)
  const atClose = fill('2024-03-04 09:30', 'buy', 7770.5, 15)
  const reading = read(rules, [held, atClose], new Set([atClose.id]),
    { time: '2024-03-04 09:30', high: 7770.75, low: 7750 })
  assert.equal(reading.balance, 50000)
  assert.equal(reading.equity, 48087.5, '只有原来那 15 手挨 7750')
}

// ---- 3) 整段仓位在本根收盘平掉，仍要挨极值 ----
{
  const open = fill('2024-03-04 09:00', 'buy', 4000, 5)
  const close = fill('2024-03-04 09:05', 'sell', 3950, 5, -250)
  const reading = read(rules, [open, close], new Set([close.id]),
    { time: '2024-03-04 09:05', high: 4000, low: 3900 })
  assert.equal(reading.balance, 49750)
  assert.equal(reading.equity, 47500, '50000 − 100×5×5：插针那一下它还在手上')
}

// ---- 4) 持仓重放：加仓摊均价、反手换成本 ----
{
  const add = replay([fill('2024-03-04 09:00', 'buy', 4000, 1), fill('2024-03-04 09:05', 'buy', 4010, 1)])
  assert.equal(add.quantity, 2)
  assert.equal(add.averagePrice, 4005, '同向加仓按手数加权')

  const flip = replay([fill('2024-03-04 09:00', 'buy', 4000, 1), fill('2024-03-04 09:05', 'sell', 4010, 3)])
  assert.equal(flip.quantity, -2)
  assert.equal(flip.averagePrice, 4010, '反手之后剩下的仓成本就是本笔成交价')
}

// ---- 5) 亏损线：越过冻结水位之后不再上移 ----
assert.equal(floorOf(rules, 50500), 48500)
assert.equal(floorOf(rules, 53000), 50100, '峰值越 52100 之后冻在 52100 − 2000')

// ---- 6) 交易日归属：06:00 之前算前一天，04:00–06:00 不属于任何一天 ----
assert.equal(tradingDayOf('2026-05-26 09:05'), '2026-05-26')
assert.equal(tradingDayOf('2026-05-27 03:55'), '2026-05-26', '凌晨那一刀归前一天')
assert.equal(tradingDayOf('2026-05-27 04:00'), null, '禁交易时段的收尾不属于任何交易日')
assert.equal(tradingDayOf('2026-05-27 05:55'), null)
assert.equal(tradingDayOf('2026-05-27 06:00'), '2026-05-27')

console.log('bot-oracle-check: 6 组断言通过（对账器和后端同一组数）')
