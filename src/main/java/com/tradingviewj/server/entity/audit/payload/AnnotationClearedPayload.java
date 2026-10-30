package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 一次清掉当前盘面的<strong>全部</strong>图形（{@link ReplayAction#ANNOTATION_CLEARED}）。
 * 对应 Go 的 {@code AnnotationClearedPayload}。
 *
 * <p>为什么不连发 N 条 {@link AnnotationDeletedPayload}：那条路会在操作记录里留下 N 行噪音，
 * 而且中途断线会留下「删了一半」的状态。一次清空 = 一条广播 = 一个原子状态。
 *
 * <p>{@code count} 和 {@code annotationIds} 说的是同一件事，两个都留着是因为用途不同：
 * 按钮回执显示「已清除 5 个」只要 count，而事后回看「清掉的是哪 5 个」只能靠编号 ——
 * 只给数量的话，历史里就只剩「有人清了 5 个」。
 */
public record AnnotationClearedPayload(
        int count,
        List<String> annotationIds
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ANNOTATION_CLEARED;
    }
}
