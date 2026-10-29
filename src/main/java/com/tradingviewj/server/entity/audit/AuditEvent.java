package com.tradingviewj.server.entity.audit;

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
 * 一条操作记录。<strong>只追加，永不修改、永不删除</strong>。
 *
 * <p><strong>职责边界</strong>：它回答「谁在什么时候对哪个 PF 账户做了什么」。
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
 * <p>两个枚举字段（{@link #actor} / {@link #action}）在库里存的是 <strong>Java 常量名</strong>
 * （{@code HUMAN} / {@code BAR_ADVANCED}）。协议上的小写串由输出侧映射，持久化不跟展示格式走 ——
 * 见 {@link com.tradingviewj.server.enums.Actor} 的说明。
 */
@Getter
@Entity
@Table(name = "audit_event")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AuditEvent extends BaseEntity {

    /**
     * 属于哪个 PF 账户。<strong>必有值</strong>，没有「不属于任何账户的操作」。
     *
     * <p><strong>故意不加外键</strong>，和别的表不一样：账户被删之后这里仍然保留
     * 原来的 id。理由是「只追加」—— 任何 {@code ON DELETE} 动作都是一次改写
     * （{@code SET NULL} 改归属、{@code CASCADE} 删行），而审计流水的全部价值
     * 就在于它没被改过。指向一个已经不存在的账户 id 不丢任何信息，
     * 反而记住了「当初算在谁头上」。
     *
     * <p>代价：原本用 NULL 表达的「全局操作」（例如新建标的）现在必须有归属。
     * 需要这类操作时，应当新建一个专门的账户来承载，而不是把这一列放开。
     */
    @Column(nullable = false)
    private String accountId;

    /** 账户内的自增序号，从 0 起。断档就说明某次广播没落库。 */
    @Column(nullable = false)
    private int seq;

    /** 谁做的。 */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Actor actor;

    /** 做了什么。 */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private ReplayAction action;

    /** 这次操作的原始负载（JSON 文本）。 */
    @Column
    private String payloadJson;

    /**
     * 记一条操作流水。
     *
     * @param accountId 算在哪个 PF 账户头上，<strong>不能为空</strong>
     * @throws IllegalArgumentException 没给账户
     */
    public static AuditEvent of(String accountId, int seq, Actor actor, ReplayAction action, String payloadJson) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("操作记录必须归属一个 PF 账户，收到: " + accountId);
        }
        AuditEvent event = new AuditEvent();
        event.accountId = accountId;
        event.seq = seq;
        event.actor = actor;
        event.action = action;
        event.payloadJson = payloadJson;
        return event;
    }
}
