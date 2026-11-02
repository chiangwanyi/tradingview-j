package com.tradingviewj.server.ws;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.tradingviewj.server.dto.AnnotationPointView;
import com.tradingviewj.server.dto.AnnotationStyleView;

/**
 * 一条画图形 / 挪图形的命令负载（{@code annotation_added} / {@code annotation_moved} /
 * {@code annotation_styled} 三条命令共用这个结构）。
 *
 * <p>三条命令字段相同而分成三种动作，理由见 {@code AnnotationStyledPayload}：
 * 样式是外观、端点是位置，事后回看要分得清「这条线是被挪过，还是只换了个颜色」。
 *
 * <p><strong>{@code id} 由前端给</strong>（{@code annotation_added} 时），不带就后端生成。
 * 这和别的实体相反，但这个聚合本来就是这个约定：图形画下去那一刻界面上就要有它
 * （选中、拖动命中测试都按 id），等一个往返还回 id 会让新线在那一瞬间点不中。
 * 后端仍然校验 id 不重复 —— 重号的后果是「拖一条线，另一条跟着动」。
 *
 * @param snapped 端点是否吸附过某根 K 线的 OHLC（界面上画个小圆点提示）
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public record AnnotationCommand(
        String id,
        /** trend / ray / rect / arrow / fib */
        String kind,
        AnnotationPointView start,
        AnnotationPointView end,
        AnnotationStyleView style,
        Boolean snapped
) {
}
