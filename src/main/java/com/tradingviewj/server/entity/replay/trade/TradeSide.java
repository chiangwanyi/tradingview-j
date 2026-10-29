package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 一批仓位的开仓方向（{@code ClosedLot.side}、{@code TradeStats.side}）。
 *
 * <p>没有 flat：只有真的开出过仓、又被平掉的那一批才会进这两处数据。
 */
public enum TradeSide {

    @JsonProperty("long")
    LONG,

    @JsonProperty("short")
    SHORT
}
