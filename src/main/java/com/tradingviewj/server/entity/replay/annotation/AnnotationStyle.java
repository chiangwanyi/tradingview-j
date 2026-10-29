package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个图形自己的样式（对应 Go 的 {@code replay.AnnotationStyle}）。
 *
 * <p>它是**画下去那一刻从模板复制过来的快照**：之后再改模板、删模板，已经画好的图形不变。
 * 存进标注而不是只存 templateId，否则模板一改，昨天画在盘面上的所有线一起变色。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class AnnotationStyle {

    /** 线条 / 矩形边框颜色 */
    private String color;

    /** 线宽（屏幕像素） */
    private double width;

    /** solid | dashed */
    private DashStyle dash;

    /** 矩形填充色，空 = 只画边框 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String fill;

    /** 矩形填充不透明度 0~1 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double fillOpacity;

    /** 斐波那契的几档水平（只有 fib 用） */
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    private List<FibLevel> levels;
}
