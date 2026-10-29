package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.market.Point;
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
public final class Annotation {

    /** 图形编号（ann-1 这种），迁移存档时用来对号 */
    private String id;

    /** 图形种类：决定 start/end 怎么解读 */
    private AnnotationKind kind;

    /** 起点：时间锚在一根已揭示的 K 线上，价格是坐标 */
    private Point start;

    /** 终点：矩形是对角，其余是一根线的另一端 */
    private Point end;

    /** 画下那一刻从模板复制的样式快照，之后改模板不影响它 */
    private AnnotationStyle style;

    /** 端点吸附过 OHLC（界面上画小圆点提示） */
    private boolean snapped;

    /** 谁画的（human / bot / system），操作记录里显示用 */
    private String createdBy;

    /** Unix 毫秒 */
    private long createdAt;

    /**
     * 0.5.2 及以前唯一的颜色字段。只在读旧存档时有用：迁移时并进 style.color 后清空，
     * 所以新标注的 JSON 里没有它。
     */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String legacyColor;
}
