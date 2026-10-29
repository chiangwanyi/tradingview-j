package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.converter.PositionTiersConverter;
import jakarta.persistence.Column;
import jakarta.persistence.Convert;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;
import java.util.List;

/**
 * Prop Firm 的考核规则一整套。
 *
 * <p>做成 {@code @Embeddable} 而不是「模板有规则、账户查模板」：因为账户上的规则必须是
 * <strong>开账户那一刻的快照</strong>。模板是「我打算用哪套考核」，账户是「我当初签的是哪一套」——
 * 后者不能因为模板改了而变。这和图形样式从模板复制是同一个道理，所以两个实体各自
 * 嵌一份 {@code PropFirmRules}，代码不重复、数据各自独立。
 *
 * <p><strong>规则是"一个阶段"的规则，不是"一个账户"的规则。</strong>
 * LucidFlex 50K 的 EVAL 和 FUNDED 是两套完全不同的东西：
 *
 * <ul>
 *   <li>EVAL：有 {@code profitTarget}、有 {@code consistencyRatio}；没有盈利日要求。</li>
 *   <li>FUNDED：{@code profitTarget} 和 {@code consistencyRatio} 都是 {@code null}
 *       （没有目标、不查一致性），但有 {@code minProfitDays} + {@code profitDayThreshold}。</li>
 * </ul>
 *
 * <p>所以 {@code null} 在这类里<strong>是有意义的取值</strong>，不是「漏配」：
 * {@code profitTarget == null} 读作「这个阶段没有盈利目标」，不是「目标为 0」。
 *
 * <p><strong>本类只定义字段，不含任何计算</strong>。「亏损线怎么上移、一致性怎么抬高目标、
 * 达到哪一档给几手」全部在 {@code service} 层（{@code PropFirmSession} /
 * {@code PropFirmPositionLimit}）。按 AGENTS.md 的要求，口径必须由用户确认后实现，
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

    /** 名义账户规模，例如 50000。升级到下一阶段时，余额重置成它。 */
    @Column(name = "account_size")
    private BigDecimal accountSize;

    /** 盈利目标（美元）。{@code null} = 这个阶段没有盈利目标（FUNDED）。 */
    @Column(name = "profit_target")
    private BigDecimal profitTarget;

    /** 最大回撤（美元）。按 {@link #drawdownType} 的口径计算。 */
    @Column(name = "max_drawdown")
    private BigDecimal maxDrawdown;

    /** 回撤按什么基准算。 */
    @Column(name = "drawdown_type")
    @Enumerated(EnumType.STRING)
    private DrawdownType drawdownType;

    /**
     * 亏损线的冻结水位：<strong>日终余额</strong>达到这个数之后，亏损线不再上移。
     *
     * <p>LucidFlex 50K 是 52100 —— 到那一天的日终余额之后，亏损线停在 50100
     * （= 52100 − 最大回撤 2000），此后赚多少这条线都不动了。
     *
     * <p>注意它<strong>不等于</strong>盈利目标：52100 和 3000 这个目标没有任何算术关系，
     * 是两条独立的规则。所以它是独立字段，不能从 {@code profitTarget} 推。
     */
    @Column(name = "trailing_lock_level")
    private BigDecimal trailingLockLevel;

    /**
     * 一致性上限（0~1，例如 0.5）。
     *
     * <p>含义：<strong>单日最大净盈利 / 阶段总净盈利</strong>不得高于它。
     * 高了不判失败，而是把通关目标抬到 {@code 单日最大净盈利 / consistencyRatio}。
     *
     * <p>{@code null} = 这个阶段不查一致性（FUNDED 就是这样）。
     */
    @Column(name = "consistency_ratio")
    private BigDecimal consistencyRatio;

    /** 需要几个「盈利日」才能升到下一阶段（FUNDED = 5）。{@code null} = 不需要。 */
    @Column(name = "min_profit_days")
    private Integer minProfitDays;

    /** 一个交易日要被算作「盈利日」，净盈利至少要这么多（150）。 */
    @Column(name = "profit_day_threshold")
    private BigDecimal profitDayThreshold;

    /**
     * 净持仓手数上限的分档表，按 {@code fromProfit} 升序。
     *
     * <p>EVAL 只有一档（0 → 40）；FUNDED 是三档（0 → 20、1000 → 30、2000 → 40）。
     * 空列表 = 这条规则不限手数。
     */
    @Convert(converter = PositionTiersConverter.class)
    @Column(name = "position_tiers_json")
    private List<PositionTier> positionTiers = List.of();

    public PropFirmRules(BigDecimal accountSize, BigDecimal profitTarget, BigDecimal maxDrawdown,
                         DrawdownType drawdownType, BigDecimal trailingLockLevel,
                         BigDecimal consistencyRatio, Integer minProfitDays,
                         BigDecimal profitDayThreshold, List<PositionTier> positionTiers) {
        this.accountSize = accountSize;
        this.profitTarget = profitTarget;
        this.maxDrawdown = maxDrawdown;
        this.drawdownType = drawdownType;
        this.trailingLockLevel = trailingLockLevel;
        this.consistencyRatio = consistencyRatio;
        this.minProfitDays = minProfitDays;
        this.profitDayThreshold = profitDayThreshold;
        this.positionTiers = positionTiers == null ? List.of() : List.copyOf(positionTiers);
    }

    /**
     * 复制一份。
     *
     * <p>必须复制而不是直接赋值：账户和模板嵌的是同一套字段，直接 {@code account.rules = template.rules}
     * 会让两个实体的 embeddable 指向<strong>同一个 Java 对象</strong>，改账户参数等于悄悄改了模板。
     * 这种 bug 在单测里几乎看不出来，只有生产上「改了一个账户，所有同模板账户都变了」时才会暴露。
     *
     * <p>{@link PositionTier} 是不可变 record，{@code List.copyOf} 得到的也是不可变列表，
     * 所以复制完之后和原对象没有任何共享的可变状态。
     */
    public PropFirmRules copy() {
        return new PropFirmRules(accountSize, profitTarget, maxDrawdown, drawdownType,
                trailingLockLevel, consistencyRatio, minProfitDays, profitDayThreshold, positionTiers);
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
        /** 跟踪日终：从每日收盘余额的最高点往下算。LucidFlex 用的是这个。 */
        TRAILING_EOD
    }
}
