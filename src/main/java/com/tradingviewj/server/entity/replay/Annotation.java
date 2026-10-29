package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 画在画布上的一个图形（对应 Go 的 {@code replay.Annotation}）。
 *
 * <p>五种图形都用「起点/终点」两个点表示：趋势线/射线/箭头是两点连成的一条线，
 * 矩形用两个对角点，斐波那契用两点的价格区间摊开几条水平线。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Annotation {

    private String id;

    /** trend | ray | rect | arrow | fib */
    private String kind;

    private Point start;

    private Point end;

    private AnnotationStyle style;

    /** 端点吸附过 OHLC（界面上画小圆点提示） */
    private boolean snapped;

    private String createdBy;

    private long createdAt;

    /**
     * 0.5.2 及以前唯一的颜色字段。只在读旧存档时有用：迁移时并进 style.color 后清空，
     * 所以新标注的 JSON 里没有它。
     */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String legacyColor;
}
