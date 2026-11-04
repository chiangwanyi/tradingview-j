package com.tradingviewj.server.entity.drawing;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;

/**
 * 一个图形自己的外观。
 *
 * <p>它是<strong>画下去那一刻从模板复制过来的快照</strong>，不是对模板的引用。
 * 这是刻意的：模板是「我常用的那套线」，改色、删模板都不该让昨天画在盘面上的线一起变色。
 * 代价是一个图形存一份样式（十几字节），换来的是「画下去的样子永远和当时一样」。
 *
 * <p>同样地，这个类只放<strong>线</strong>的样式：斐波那契的四条水平线是
 * {@link Drawing#getFibLevels()}，因为它们有数量（0~n 条），是集合不是标量。
 */
@Getter
@Embeddable
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class DrawingStyle {

    /** 线条 / 矩形边框颜色，{@code #rrggbb}。 */
    @Column
    private String color;

    /** 线宽（屏幕像素）。 */
    @Column
    private BigDecimal width;

    @Enumerated(EnumType.STRING)
    @Column
    private Dash dash;

    /** 矩形填充色；NULL = 只画边框。 */
    @Column
    private String fill;

    /** 填充不透明度 0~1。 */
    @Column
    private BigDecimal fillOpacity;

    public DrawingStyle(String color, BigDecimal width, Dash dash, String fill, BigDecimal fillOpacity) {
        this.color = color;
        this.width = width;
        this.dash = dash;
        this.fill = fill;
        this.fillOpacity = fillOpacity;
    }

    public enum Dash {
        SOLID, DASHED
    }
}
