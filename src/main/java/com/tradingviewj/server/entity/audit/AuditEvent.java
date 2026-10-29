package com.tradingviewj.server.entity.audit;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.enums.ReplayAction;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 一条操作记录。<strong>只追加，永不修改、永不删除</strong>。
 *
 * <p><strong>职责边界</strong>：它回答「谁在什么时候对哪块盘面做了什么」。
 * 它<strong>不是</strong>业务状态的来源 —— 图形、挂单、成交各自有自己的表。
 * 这条分界很重要：如果靠重放操作记录来还原状态，那么一次转发/合并出错就会让
 * 整块盘面的状态永久错位；而独立存表的话，最坏情况是「审计流水少了一条」，
 * 盘面本身还是对的。
 *
 * <p>{@link #payloadJson} 存原始广播负载的 JSON 文本，而不是像 Go 版那样用
 * {@code any}（Java 里是 {@code Object}）。用 {@code Object} 的代价是：反序列化时
 * 不知道该还原成哪个类，只能全存成 {@code Map}，读的时候到处强转、到处判空。
 * 存字符串 + 一个类型化的 {@link #action}，读的一方按 action 自己挑 DTO 解析。
 *
 * <p>两个枚举字段（{@link #actor} / {@link #action}）在库里存的是<strong>小写线值</strong>
 * （{@code "human"} / {@code "bar_advanced"}），不是 Java 常量名 —— 见各自的枚举说明。
 */
@Getter
@Entity
@Table(name = "audit_event")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AuditEvent extends BaseEntity {

    /** 属于哪块盘面；NULL = 全局操作（例如新建标的）。 */
    @Column
    private String sessionId;

    /** 会话内的自增序号，从 0 起。断档就说明某次广播没落库。 */
    @Column(nullable = false)
    private int seq;

    /** 谁做的。 */
    @Column(nullable = false)
    private Actor actor;

    /** 做了什么。 */
    @Column(nullable = false)
    private ReplayAction action;

    /** 这次操作的原始负载（JSON 文本）。 */
    @Column
    private String payloadJson;

    public static AuditEvent of(String sessionId, int seq, Actor actor, ReplayAction action, String payloadJson) {
        AuditEvent event = new AuditEvent();
        event.sessionId = sessionId;
        event.seq = seq;
        event.actor = actor;
        event.action = action;
        event.payloadJson = payloadJson;
        return event;
    }
}
