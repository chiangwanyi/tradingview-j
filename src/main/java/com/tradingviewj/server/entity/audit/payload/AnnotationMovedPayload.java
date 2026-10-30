package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.enums.ReplayAction;

/**
 * 拖动了一个图形的端点（{@link ReplayAction#ANNOTATION_MOVED}）。对应 Go 的 {@code AnnotationPayload}。
 *
 * <p>发的永远是<strong>移动之后</strong>的图形：一次拖动会产生很多次预览位置的更新，
 * 但只有松手那一下进审计流水，所以每条负载都是一个落定的状态，回放时不需要「重放拖拽过程」。
 *
 * <p>和 {@link AnnotationAddedPayload} 字段完全相同 —— 分两个类型只为了让动作和类型一一对应，
 * {@code switch} 上不必再区分「加的」还是「挪的」。
 */
public record AnnotationMovedPayload(
        Drawing annotation
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ANNOTATION_MOVED;
    }
}
