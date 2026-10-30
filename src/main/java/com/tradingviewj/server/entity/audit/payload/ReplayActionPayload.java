package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

/**
 * 一个动作的负载。<strong>每一个 {@link ReplayAction} 取值恰好对应一个实现</strong>，
 * 由 {@link #action()} 认领。
 *
 * <p>是 {@code sealed} 的理由：动作集合是封闭的（新增动作要先加枚举值，见
 * {@link ReplayAction} 的说明），所以 {@code switch} 可以穷尽 —— 将来真要做类型化读取，
 * 漏掉一个动作是编译错误，不是运行时静默地落进 {@code default} 分支。
 *
 * <p>这层<strong>只描述结构</strong>：没有校验、没有换算、没有序列化。wire 上是什么样，
 * 这些 record 就长什么样（映射规则见包说明）。
 */
public sealed interface ReplayActionPayload
        permits BarAdvancedPayload, BarExtendedPayload,
                AnnotationAddedPayload, AnnotationMovedPayload, AnnotationStyledPayload,
                AnnotationDeletedPayload, AnnotationClearedPayload,
                OrderPlacedPayload, OrderFilledPayload, OrderMovedPayload, OrderCancelledPayload,
                FlatAllPayload,
                ControlChangedPayload, MembersChangedPayload,
                BotStartedPayload, BotStoppedPayload,
                VoiceAddedPayload, VoiceClearedPayload,
                RecordingAudioPayload, BasePayload {

    /** 这条负载属于哪个动作。 */
    ReplayAction action();
}
