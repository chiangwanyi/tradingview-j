package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 订单状态（对应 Go 的 {@code OrderOpen} / {@code OrderFilled} / {@code OrderCancelled}）。
 *
 * <p>只有三个取值：挂单列表里永远是 open，成交或被撤之后就不再变化。
 */
public enum OrderStatus {

    @JsonProperty("open")
    OPEN,

    @JsonProperty("filled")
    FILLED,

    @JsonProperty("cancelled")
    CANCELLED
}
