package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 下单方向（对应 Go 的 {@code replay.Order.Side} 的取值约定）：买 / 卖。
 *
 * <p>注意它和 {@link TradeSide} 不是一套词：这里说的是「这一单是买还是卖」，
 * 那里说的是「这一批仓是开多还是开空」。下买单可能是平空，两者不能互换。
 */
public enum OrderSide {

    @JsonProperty("buy")
    BUY,

    @JsonProperty("sell")
    SELL
}
