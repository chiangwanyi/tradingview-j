package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 图形的种类（对应 Go 的 {@code Annotation.Kind}，取值靠注释约定，没有常量）。
 *
 * <p>线上与存档里都是小写单词，前端用 {@code kind === 'trend'} 比较，所以每个常量
 * 都钉了 {@code @JsonProperty}：改名不注意就会让已经画好的图形从画布上消失。
 *
 * <p>顺序与前端工具栏一致：五种图形都只用「起点/终点」两个点表示。
 */
public enum AnnotationKind {

    @JsonProperty("trend")
    TREND,

    @JsonProperty("ray")
    RAY,

    @JsonProperty("rect")
    RECT,

    @JsonProperty("arrow")
    ARROW,

    @JsonProperty("fib")
    FIB
}
