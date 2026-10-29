package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;
import java.time.Instant;

/**
 * 阶段流水：这个账户<strong>当过哪几个阶段的什么身份</strong>。
 *
 * <p>因为三个阶段挤在<strong>同一行账户</strong>上（{@code stage} 只有一个当前值），
 * 历史必须另外留档，否则「我这个 FUNDED 是考来的还是买来的」「EVAL 当时赚了多少」
 * 这两类问题在事后就无从回答 —— 账户行只记当前，不记过去。
 *
 * <p><strong>行数就说明身份</strong>：
 *
 * <ul>
 *   <li>用 FUNDED 模板直接建户 → 只有 1 行（{@code EVAL} 那一行从来不存在）；</li>
 *   <li>考过来的 → 2 行（EVAL 关了、FUNDED 开着）。</li>
 * </ul>
 *
 * <p>所以「创建时就是 FUNDED」不需要一个额外字段来标记，看行数就知道 ——
 * 加一个 {@code wasPromoted} 布尔值只会多一个能和行数打架的真相。
 *
 * <p><strong>结束原因和进入原因分开记</strong>：{@code PROMOTED} 这两个字在两头都成立
 * （这一行是被升上来的 vs 这一行是被升走的），塞进一列会让「谁升谁」变得要靠猜。
 *
 * <p>{@code accountId} 按本文件同类约定不加外键：它是这个账户的生命记录，
 * 删账户时保留可以审计（和成交、图形一样，删的是统计点，不是数据）。
 */
@Getter
@Entity
@Table(name = "prop_firm_stage_log")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmStageLog extends BaseEntity {

    @Column(nullable = false)
    private String accountId;

    /** 账户内连续，从 0 起。用于排序和「最后一行就是当前阶段」。 */
    @Column(nullable = false)
    private int seq;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private PropFirmStage stage;

    @Column(nullable = false)
    private Instant startedAt;

    /** {@code null} = 就是当前所处阶段。 */
    private Instant endedAt;

    /** 进入这个阶段时的余额基准。 */
    @Column(nullable = false)
    private BigDecimal baseBalance;

    /** 离开那一刻的余额。 */
    private BigDecimal balanceAtEnd;

    /** 该阶段净盈亏 = {@code balanceAtEnd − baseBalance}。 */
    private BigDecimal netPnl;

    /** 该阶段单日最大净盈利 —— 一致性就是拿它和总净盈利比的，留档便于复核当时的判定。 */
    private BigDecimal maxDailyProfit;

    /** 该阶段达标盈利日数。 */
    private Integer profitDays;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Reason reason;

    @Enumerated(EnumType.STRING)
    private EndReason endReason;

    /**
     * 开一段阶段记录。
     *
     * @param seq         账户内序号（第一条是 0）
     * @param stage       进入的阶段
     * @param baseBalance 进入时的余额基准 —— 复制进来看似冗余，但账户升级之后就查不到
     *                    「EVAL 当时是从多少钱开始的」了
     * @param reason      这一行为什么存在
     */
    public static PropFirmStageLog begin(String accountId, int seq, PropFirmStage stage,
                                        BigDecimal baseBalance, Instant at, Reason reason) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("阶段流水必须属于一个 PF 账户，收到: " + accountId);
        }
        PropFirmStageLog log = new PropFirmStageLog();
        log.accountId = accountId;
        log.seq = seq;
        log.stage = stage;
        log.startedAt = at;
        log.baseBalance = baseBalance;
        log.reason = reason;
        return log;
    }

    /**
     * 收尾：把这个阶段的账结掉。
     *
     * <p>只对还开着的行有效（{@link #endedAt} 已经有值就什么都不做）——
     * 重复收尾意味着上游在同一个阶段上判了两次，那种错不该由本类静默吸收，
     * 但也不该抛异常把一次成功的升级回滚掉，所以选择幂等。
     */
    public void close(EndReason endReason, BigDecimal balanceAtEnd, BigDecimal maxDailyProfit,
                      Integer profitDays, Instant at) {
        if (this.endedAt != null) {
            return;
        }
        this.endedAt = at;
        this.endReason = endReason;
        this.balanceAtEnd = balanceAtEnd;
        this.maxDailyProfit = maxDailyProfit;
        this.profitDays = profitDays;
        this.netPnl = balanceAtEnd == null ? null : balanceAtEnd.subtract(baseBalance);
    }

    /** 这一行为什么存在。 */
    public enum Reason {
        /** 建户时就是这个阶段（FUNDED 模板直接开出来的账户会走这里）。 */
        CREATED,
        /** 从上一个阶段升上来的。 */
        PROMOTED
    }

    /** 这个阶段怎么结束的。 */
    public enum EndReason {
        /** 达标，升到下一阶段。 */
        PROMOTED,
        /** 撞了亏损线。 */
        FAILED,
        /** 走到 PAYOUT，结束。 */
        PAYOUT_DONE
    }
}
