package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

/**
 * 删掉了一个图形（{@link ReplayAction#ANNOTATION_DELETED}）。对应 Go 的
 * {@code AnnotationDeletedPayload}。
 *
 * <p>只带编号，不带被删的那个图形：删掉之后它就是历史，回放要的是「这一条不在了」，
 * 而不是一份留着它全部样式的副本。真要追「删掉的是什么样」，那是另一个需求
 * （撤销 / 回收站），而操作记录是 append-only，不承担撤销。
 */
public record AnnotationDeletedPayload(
        String annotationId
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ANNOTATION_DELETED;
    }
}
