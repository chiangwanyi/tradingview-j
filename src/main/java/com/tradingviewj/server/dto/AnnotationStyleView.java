package com.tradingviewj.server.dto;

import java.math.BigDecimal;
import java.util.List;

/**
 * 图形 / 模板<strong>在协议里</strong>的样式（前端 {@code webapp/src/types/protocol.ts} 的
 * {@code AnnotationStyle}）。
 *
 * <p>和 {@link OrderView} 是同一个理由：实体 {@code DrawingStyle} 存的是库里的形状
 * （{@code Dash} 枚举存常量名 {@code SOLID}、宽度是 {@code BigDecimal}），
 * 协议要的是客户端的形状（小写 {@code solid}）。换算只放在这里一处。
 *
 * <p><strong>斐波那契的四条水平线在这里面，不在 {@link AnnotationView} 的同级</strong>：
 * 对前端来说它们是「这套样式的一部分」（换模板连四条线的颜色一起换），
 * 而 {@code Drawing#getFibLevels()} 在实体上是独立字段 —— 因为库里它有数量、要占行。
 * 存储形状和协议形状不同，正是 DTO 存在的理由。
 *
 * @param color       {@code #rrggbb}
 * @param width       线宽（屏幕像素），1~6
 * @param dash        {@code solid} / {@code dashed}
 * @param fill        矩形填充色；{@code null} = 只画边框（全局 non_null 会省掉这个键）
 * @param fillOpacity 填充不透明度 0~1，只有矩形有意义
 * @param levels      斐波那契四条水平线；只有 {@code fib} 才有，其它图形为 {@code null}
 */
public record AnnotationStyleView(
        String color,
        BigDecimal width,
        String dash,
        String fill,
        BigDecimal fillOpacity,
        List<FibLevelView> levels
) {

    /**
     * 一条水平线。{@code ratio} 只认 -1 / 0 / 1 / 2，由 service 固定住顺序和取值 ——
     * 前端拿到的永远正好四条、永远按这个顺序。
     */
    public record FibLevelView(int ratio, String color) {
    }
}
