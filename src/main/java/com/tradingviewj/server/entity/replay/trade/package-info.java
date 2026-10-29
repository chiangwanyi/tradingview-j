/**
 * 交易：订单、成交、持仓、盈亏统计，以及下单体、改单、全平这些动作的负载。
 *
 * <p>`OrderFilledPayload` 依赖 `ClosedLot`/`Order`/`Position`/`Trade`，`FlatAllPayload` 依赖两个订单负载——
 * 放在同一个包里，读的人不用跨四个包拼图。
 */
package com.tradingviewj.server.entity.replay.trade;
