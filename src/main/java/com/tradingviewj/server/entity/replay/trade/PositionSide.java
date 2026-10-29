package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 净头寸的方向（对应 Go 的 {@code replay.Position.Side}）。
 *
 * <p>{@link #FLAT} 不是「从没开过仓」，而是「方向和手数都归零」——平完仓之后依然是 flat，
 * 不是 null：前端据此决定要不要画那条成本线。
 */
public enum PositionSide {

    @JsonProperty("long")
    LONG,

    @JsonProperty("short")
    SHORT,

    @JsonProperty("flat")
    FLAT
}
