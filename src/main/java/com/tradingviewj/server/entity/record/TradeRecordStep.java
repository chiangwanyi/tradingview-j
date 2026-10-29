package com.tradingviewj.server.entity.record;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.enums.ReplayAction;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 交易记录里的一步 = <strong>一次真实操作</strong>。
 *
 * <p>第 0 步是起点（{@link ReplayAction#BASE}）：它就是「开仓前那一屏」，
 * 没有事件，只有那一刻的完整盘面快照（存在 {@link #payloadJson} 里）。
 * 之后每一条放行的广播开一步，所以「第 N 步」和「用户第 N 次动手」永远对得上：
 * 点一次【下一根】、画一个图形、拖一次挂单价、录一段音，各占一步。
 *
 * <p>语音步是 {@link ReplayAction#RECORDING_AUDIO}：{@link #payloadJson} 为空
 * （它不改盘面），那一段在 {@link #audioClipId} 里。回放到这一步画面不动，但语音该响。
 *
 * <p><strong>为什么单独一张表而不是记录里的一个 JSON 数组</strong>：一步一条记录能被
 * 单独查询和分页，回放时可以按 {@code step_no} 一跳一跳地取，不必把几百步全部读进内存。
 * 代价是一次 JOIN 或若干次小查询，收益是「打开一条交易记录」的代价和它的长度无关。
 */
@Getter
@Entity
@Table(name = "trade_record_step")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class TradeRecordStep extends BaseEntity {

    @Column(nullable = false)
    private String recordId;

    /** 从 0 起。0 是起点。 */
    @Column(nullable = false)
    private int stepNo;

    /** 这一步是什么。和 {@code AuditEvent.action} 共用同一套取值。 */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private ReplayAction kind;

    /** 这一步发生时<strong>当前那根</strong> K 线的时间。 */
    @Column(nullable = false)
    private String barTime;

    /** 这一步发生后的游标（跳步时用作自检）。 */
    @Column(nullable = false)
    private int cursorIndex;

    /** 谁动的。 */
    @Enumerated(EnumType.STRING)
    @Column
    private Actor actor;

    /** 仅语音步：挂在这一步上的那一段语音。 */
    @Column
    private String audioClipId;

    /**
     * 这一步的事件负载（JSON 文本）。
     *
     * <p>留成数组形态而不是单对象：协议层不该假设「一步永远只有一条事件」。
     * 语音步为空。
     */
    @Column
    private String payloadJson;

    public static TradeRecordStep of(String recordId, int stepNo, ReplayAction kind, String barTime,
                                     int cursorIndex, Actor actor, String audioClipId, String payloadJson) {
        TradeRecordStep step = new TradeRecordStep();
        step.recordId = recordId;
        step.stepNo = stepNo;
        step.kind = kind;
        step.barTime = barTime;
        step.cursorIndex = cursorIndex;
        step.actor = actor;
        step.audioClipId = audioClipId;
        step.payloadJson = payloadJson;
        return step;
    }
}
