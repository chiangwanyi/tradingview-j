package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.enums.ReplayAction;

/**
 * 改了已画好图形的样式（{@link ReplayAction#ANNOTATION_STYLED}）。对应 Go 的 {@code AnnotationPayload}。
 *
 * <p>「换模板」和「拖端点」在审计流水里是两条动作，因为它们改的是图形身上两种不相干的东西：
 * 样式是外观（颜色/线宽/虚线/填充），端点是位置（价格与哪根 K 线）。混成一条的话，
 * 事后回看没法回答「这条线是被挪过，还是只是换了个颜色」。
 *
 * <p>负载同样是<strong>改完之后</strong>的图形，端点保持原样。
 */
public record AnnotationStyledPayload(
        Drawing annotation
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ANNOTATION_STYLED;
    }
}
