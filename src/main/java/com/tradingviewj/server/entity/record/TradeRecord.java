package com.tradingviewj.server.entity.record;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.entity.trade.Side;
import jakarta.persistence.CollectionTable;
import jakarta.persistence.Column;
import jakarta.persistence.ElementCollection;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.OrderColumn;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

/**
 * 交易记录：<strong>一笔完整交易</strong>——开仓前那一屏 + 之后每一步操作 + 结算摘要。
 *
 * <p>名字不叫 recording / 录像：它确实不是视频，是「操作流水 + 结算摘要」。
 * 回放就是把起点深拷贝一份，然后按顺序把每一步重新演一遍 —— 和实时界面用的是同一套
 * 状态更新逻辑，所以回放看到的画面和当时看到的必然一模一样，包括机器人做的操作。
 *
 * <p><strong>职责边界</strong>：
 *
 * <ul>
 *   <li>摘要字段（方向、进出价、手数、盈亏、MFE/MAE）是<strong>收尾时算好写进来的</strong>。
 *       它们冗余于 {@code trade} 表，但值得冗余：交易记录列表要按盈亏排序、按标签筛选，
 *       每次列表都去重放几百笔成交是不可接受的。写入是一次性的，之后这笔交易就封存了。</li>
 *   <li>它<strong>不持有成交</strong>。成交在 {@code trade} 表里，靠 {@code trade.trade_record_id}
 *       反向引用 —— 因为成交先发生、记录后收尾，硬做成「记录包含成交」就需要先有记录再有成交，
 *       而用户是直接下单的。</li>
 *   <li>它不持有音频。语音是账户下的一级实体，记录的步骤里只存 {@code audioClipId} 指向它。</li>
 * </ul>
 *
 * <p><strong>生命周期是自动的</strong>：仓位从空仓变成有仓那一刻开始，回到空仓那一刻收尾。
 * 让用户记得点「开始/结束」的结果必然是「忘了开就没有记录」。所以没有「录制中」这个状态，
 * 也没有开关。
 *
 * <p>{@link #accountId} 只是<strong>统计归属</strong>，不是所有权：账户被删掉之后这里仍然保留
 * 原来那个 id（指向一个已经不存在的账户），打开、回放、删除记录都不依赖账户 ——
 * 记录是独立的一份数据，只有用户自己点删除才会消失。
 */
@Getter
@Entity
@Table(name = "trade_record")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class TradeRecord extends BaseEntity {

    /**
     * 属于哪个 PF 账户。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：和成交、图形一样是<strong>弱引用</strong>，
     * 账户只是统计点，记录不归它所有。删账户之后这条记录仍在，这里保留原来那个 id；
     * 想改归属用 {@link #attachTo} 显式挂到另一个账户上。
     */
    @Column(nullable = false)
    private String accountId;

    /** 起点那一屏的 K 线时间（开仓**之前**）。 */
    @Column(nullable = false)
    private String anchorBarTime;

    @Setter
    private String entryBarTime;

    @Setter
    private String exitBarTime;

    @Setter
    @Enumerated(EnumType.STRING)
    private Side side;

    @Setter
    private BigDecimal quantity;

    /** 持仓期间的最大手数（加减仓之后的峰值）。 */
    @Setter
    private BigDecimal maxQuantity;

    @Setter
    private BigDecimal entryPrice;

    @Setter
    private BigDecimal exitPrice;

    @Setter
    private BigDecimal realizedPnl;

    /** 最大有利波动（美元）。 */
    @Setter
    private BigDecimal mfe;

    /** 最大不利波动（美元）。 */
    @Setter
    private BigDecimal mae;

    @Setter
    @Column(nullable = false)
    private int barsHeld;

    /** 成交笔数（开仓 + 加减仓 + 平仓）。 */
    @Setter
    @Column(nullable = false)
    private int fillCount;

    /**
     * 这一笔是否<strong>正常平完</strong>。
     *
     * <p>false 不是数据丢了：账户被删、程序退出、进程崩溃时仓位确实还在，
     * 那条记录就是「当时还没结束」。重启后不续录（开头已经在崩溃前那次检查点里了），
     * 下一次「空仓 → 有仓」才开新的一条。
     */
    @Setter
    @Column(nullable = false)
    private boolean closed;

    /**
     * 这些操作是谁做的。在<strong>开仓那一刻</strong>就定下来存好 —— 事后按 createdBy 猜不准：
     * 真人开的盘面被机器人接管之后开出来的那一笔，来源就是机器人。
     *
     * <p>取值只可能是 {@link Actor#HUMAN} 或 {@link Actor#BOT}，这一点由
     * {@link #open} 把关（{@code SYSTEM} 用来描述系统自动收尾，不是一笔交易的来源）。
     */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Actor actor;

    /** 给人看的细分：真人是操作者名字，机器人是策略名。改名不该让筛选分组裂开。 */
    @Setter
    private String actorName;

    @Setter
    private Instant endedAt;

    /** 用户打的标签，顺序即打标签的顺序。 */
    @Setter
    @ElementCollection(fetch = FetchType.EAGER)
    @CollectionTable(name = "trade_record_tag", joinColumns = @JoinColumn(name = "record_id"))
    @OrderColumn(name = "tag_index")
    private List<RecordTag> tags = new ArrayList<>();

    /**
     * 开一条记录。
     *
     * @param accountId 这笔交易算在哪个 PF 账户上，<strong>不能为空</strong>：没有账户就不该有交易
     * @param actor     只能是 {@link Actor#HUMAN} 或 {@link Actor#BOT}
     * @throws IllegalArgumentException 没给账户，或者传了 {@code SYSTEM}
     *                                  （它不是一笔交易的来源，而是「系统自己动的手」这类操作的操作者）
     */
    public static TradeRecord open(String accountId, String anchorBarTime, Actor actor, String actorName) {
        if (actor != Actor.HUMAN && actor != Actor.BOT) {
            throw new IllegalArgumentException("交易记录的来源只能是 HUMAN 或 BOT，收到: " + actor);
        }
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("交易记录必须属于一个 PF 账户，收到: " + accountId);
        }
        TradeRecord record = new TradeRecord();
        record.accountId = accountId;
        record.anchorBarTime = anchorBarTime;
        record.actor = actor;
        record.actorName = actorName;
        return record;
    }

    /**
     * 把一条已经存在的记录挂到另一个账户上（典型场景：原来的账户被删了）。
     *
     * <p>只动归属，不动内容 —— 记录里的每一步操作都是当时发生的事实，重挂不改历史。
     */
    public void attachTo(String accountId) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("要挂到的账户不能为空，收到: " + accountId);
        }
        this.accountId = accountId;
    }

    /** 这笔交易是从哪一边开的。还没有成交时无法确定。 */
    public void openPosition(Side side, String barTime, BigDecimal price, BigDecimal quantity) {
        this.side = side;
        this.entryBarTime = barTime;
        this.entryPrice = price;
        this.quantity = quantity;
        this.maxQuantity = quantity;
    }

    /** 收尾：把仓位回到 0 那一刻的事实写进去。 */
    public void closeAt(String barTime, BigDecimal price, BigDecimal realizedPnl) {
        this.exitBarTime = barTime;
        this.exitPrice = price;
        this.realizedPnl = realizedPnl;
        this.closed = true;
        this.endedAt = Instant.now();
    }
}
