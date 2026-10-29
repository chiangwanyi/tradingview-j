package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 订单类型（对应 Go 的 {@code KindLimit} / {@code KindStop} / {@code KindMarket}）。
 *
 * <p>没有「止损单」「止盈单」：想止损就自己挂一张 STOP 单。
 * {@link #MARKET} 是唯一例外——它只在成交那一刻存在，Status 一律是 filled，
 * 记在挂单列表里是为了让成交、历史记录和撤单共用同一套结构。
 */
public enum OrderKind {

    @JsonProperty("limit")
    LIMIT,

    @JsonProperty("stop")
    STOP,

    /** 市价单：下单即按当前收盘价成交，不会留在挂单列表里 */
    @JsonProperty("market")
    MARKET
}
