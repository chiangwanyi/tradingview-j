package com.tradingviewj.server.entity.propfirm;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;

/**
 * Prop Firm 的考核规则一整套。
 *
 * <p>做成 {@code @Embeddable} 而不是「模板有规则、账户查模板」：因为账户上的规则必须是
 * <strong>开账户那一刻的快照</strong>。模板是「我打算用哪套考核」，账户是「我当初签的是哪一套」——
 * 后者不能因为模板改了而变。这和图形样式从模板复制是同一个道理，所以两个实体各自
 * 嵌一份 {@code PropFirmRules}，代码不重复、数据各自独立。
 *
 * <p><strong>⚠️ 待确认的业务规则</strong>：本类只<strong>定义字段</strong>，
 * 不含任何计算。<strong>回撤是按权益还是按余额、日内亏损按哪一天的哪一刻起算、
 * 达标后是否冻结账户</strong>—— 这些口径没有权威实现，写在这里的任何一种都只是猜测。
 * 按 AGENTS.md 的要求，这些必须在 {@code service} 层由用户确认后再实现，
 * 不允许在实体里「顺手」算一个。
 *
 * <p>金额一律 BigDecimal：考核的通过/失败判定是「差 1 美分」级别的比较，
 * 用 double 会在边界上抖动。
 */
@Getter
@Setter
@Embeddable
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmRules {

    /** 名义账户规模，例如 50000。 */
    @Column(name = "account_size")
    private BigDecimal accountSize;

    /** 盈利目标（美元），通常等于 accountSize × 目标百分比。 */
    @Column(name = "profit_target")
    private BigDecimal profitTarget;

    /** 最大回撤（美元）。按 {@link #drawdownType} 的口径计算。 */
    @Column(name = "max_drawdown")
    private BigDecimal maxDrawdown;

    /** 单日最大亏损（美元）。NULL = 这套考核不限日内亏损。 */
    @Column(name = "daily_loss_limit")
    private BigDecimal dailyLossLimit;

    /** 回撤按什么基准算。 */
    @Column(name = "drawdown_type")
    @Enumerated(EnumType.STRING)
    private DrawdownType drawdownType;

    /** 达到考核要求至少需要几个交易日。 */
    @Column(name = "min_trading_days")
    private Integer minTradingDays;

    /** 盈利分成比例 0~1，例如 0.9。 */
    @Column(name = "profit_split")
    private BigDecimal profitSplit;

    public PropFirmRules(BigDecimal accountSize, BigDecimal profitTarget, BigDecimal maxDrawdown,
                         BigDecimal dailyLossLimit, DrawdownType drawdownType, Integer minTradingDays,
                         BigDecimal profitSplit) {
        this.accountSize = accountSize;
        this.profitTarget = profitTarget;
        this.maxDrawdown = maxDrawdown;
        this.dailyLossLimit = dailyLossLimit;
        this.drawdownType = drawdownType;
        this.minTradingDays = minTradingDays;
        this.profitSplit = profitSplit;
    }

    /**
     * 复制一份。
     *
     * <p>必须复制而不是直接赋值：账户和模板嵌的是同一套字段，直接 {@code account.rules = template.rules}
     * 会让两个实体的 embeddable 指向<strong>同一个 Java 对象</strong>，改账户参数等于悄悄改了模板。
     * 这种 bug 在单测里几乎看不出来，只有生产上「改了一个账户，所有同模板账户都变了」时才会暴露。
     */
    public PropFirmRules copy() {
        return new PropFirmRules(accountSize, profitTarget, maxDrawdown, dailyLossLimit,
                drawdownType, minTradingDays, profitSplit);
    }

    /**
     * 回撤口径。
     *
     * <p>三种口径的差别正是评判「你还有没有资格继续交易」的唯一依据，
     * 所以它必须是显式字段，不能靠「大家都知道是按日内高点算」这种默契。
     */
    public enum DrawdownType {
        /** 静态：从初始余额往下算一条固定的线，赚了钱这条线也不动。 */
        STATIC,
        /** 跟踪日内：从当日权益的最高点往下算，且这个最高点只增不减。 */
        TRAILING_INTRADAY,
        /** 跟踪日终：从每日收盘权益的最高点往下算。 */
        TRAILING_EOD
    }
}
