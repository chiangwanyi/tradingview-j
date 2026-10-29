package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 线条样式（对应 Go 的 {@code AnnotationStyle.Dash} 的取值约定）。
 *
 * <p>只有实线和虚线两种：再多的样式前端画不出来，也不必为它开后门。
 */
public enum DashStyle {

    @JsonProperty("solid")
    SOLID,

    @JsonProperty("dashed")
    DASHED
}
