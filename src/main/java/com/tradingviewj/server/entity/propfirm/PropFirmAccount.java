package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Embedded;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;
import java.time.Instant;

/**
 * Prop Firm 账户：一个<strong>具体在考核中的</strong>账户。
 *
 * <p><strong>职责边界</strong>：它记录「这套考核的参数 + 现在跑到哪儿了」。
 * 它不持有成交、不持有交易记录 —— 那些靠 {@code accountId} 反向引用它。
 * 也不存「已实现盈亏」，因为那是所有归属于它的成交之和，随时能算。
 *
 * <p>{@link #rules} 是<strong>开账户时从模板复制进来的副本</strong>，
 * 不是指向模板的引用。理由见 {@link PropFirmRules}：模板改了，我签的那一份不能变。
 * {@link #templateId} 只用来回答「这个账户是从哪套模板开的」。
 *
 * <p>{@link #peakEquity} 是「历史最高权益」，跟踪型回撤全靠它。之所以<strong>存下来</strong>
 * 而不是每次从成交重算：峰值是<strong>路径依赖</strong>的 —— 重算需要知道每一个时刻的
 * 浮盈浮亏，而成交记录里没有「每一刻的持仓市值」。这是本类里唯一必须持久化的派生量。
 *
 * <p>⚠️ 待确认：{@link #status} 什么时候从 ACTIVE 变成 PASSED / FAILED、
 * {@link #tradingDays} 怎么数（跨日算一天还是有过成交才算），口径未定，
 * 本类只提供状态位，不做判定。
 */
@Getter
@Entity
@Table(name = "prop_firm_account")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmAccount extends BaseEntity {

    @Setter
    private String name;

    /** 从哪套模板开的（只用于追溯，模板删了就置 NULL）。 */
    @Column
    private String templateId;

    /** 规则快照。 */
    @Embedded
    private PropFirmRules rules;

    /** 当前余额（已结算的现金）。 */
    @Setter
    @Column(nullable = false)
    private BigDecimal balance;

    /** 历史最高权益。跟踪型回撤的基准，只增不减。 */
    @Setter
    @Column(nullable = false)
    private BigDecimal peakEquity;

    /** 已经过的交易日数。 */
    @Setter
    @Column(nullable = false)
    private int tradingDays;

    @Setter
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Status status;

    @Column(nullable = false)
    private Instant startedAt;

    /**
     * 按模板开一个账户。规则在这一刻<strong>复制</strong>进来，此后与模板无关。
     *
     * @param template 规则来源
     * @param name     账户名（用户自己起，例如「Apex 50K 第一次」）
     */
    public static PropFirmAccount open(PropFirmTemplate template, String name) {
        PropFirmAccount account = new PropFirmAccount();
        account.name = name;
        account.templateId = template.getId();
        account.rules = template.getRules().copy();
        account.balance = template.getRules().getAccountSize();
        account.peakEquity = account.balance;
        account.status = Status.ACTIVE;
        account.startedAt = Instant.now();
        return account;
    }

    public enum Status {
        /** 考核进行中。 */
        ACTIVE,
        /** 达标通过。 */
        PASSED,
        /** 触发回撤/日内亏损，失败。 */
        FAILED
    }
}
