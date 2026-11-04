package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.AnnotationView;
import com.tradingviewj.server.enums.ReplayAction;

/**
 * 画了一个图形（{@link ReplayAction#ANNOTATION_ADDED}）。对应 Go 的 {@code AnnotationPayload}。
 *
 * <p>只带<strong>那一个</strong>图形本身，不带全量清单：图形多起来之后整份列表很大，
 * 而新增只影响一条，前端 {@code push} 一下即可。这和订单/成交那边「每次发全量」的口径
 * 不同 —— 那边数量少、且一次操作会连锁改动多张单，全量更省事。
 *
 * <p>动作名是 {@code ANNOTATION_*}，类型却叫 {@code Drawing}（Go 叫 {@code Annotation}）：
 * 动作名在库里存了历史数据，不能改；类型名跟随本仓库的 {@code drawing} 聚合。
 *
 * <p><strong>负载装的是 {@link AnnotationView} 而不是 {@code Drawing} 实体</strong>
 * （2026-11-02 改）：{@code audit_event.payload_json} 的契约是「原始广播负载，原样留存」，
 * 而广播发的是协议形状（kind 小写、锚点叫 {@code time}、斐波那契四条线在 {@code style.levels} 里）。
 * 装实体的话，库里存一份、广播发另一份，两者只在「有人真去读历史」时才暴露不一致。
 */
public record AnnotationAddedPayload(
        AnnotationView annotation
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ANNOTATION_ADDED;
    }
}
