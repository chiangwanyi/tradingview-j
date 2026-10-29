package com.tradingviewj.server.entity.trade;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;

/**
 * 一笔成交 = 一次挂单被触发。
 *
 * <p><strong>这是整个交易域的唯一真相</strong>。持仓数量、持仓均价、已实现盈亏
 * 全都是这张表按顺序重放出来的结果（见 {@code service.TradeLedger}），不单独存。
 * Go 版在实例上同时存了 {@code Trades} 和 {@code Position} / {@code RealizedPnl}，
 * 两者一旦分叉就没有任何办法判断谁对 —— 现在不可能分叉，因为只有一个来源。
 *
 * <p>不做滑点：成交价通常等于挂单价，只有开盘直接跳过挂单价（跳空）时取开盘价。
 * 这是复盘场景的明确约定，不是省略。
 *
 * <p><strong>但手续费是要算的</strong>（{@link #commission}）：PF 考核的盈利目标、回撤、
 * 一致性全部按<strong>扣费后</strong>的净额判，把手续费当成"复盘不必精确"的细节，
 * 会让一整套判定都偏松 —— 而偏差会随着交易笔数累积，越到考核后期越离谱。
 */
@Getter
@Entity
@Table(name = "trade")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Trade extends BaseEntity {

    /**
     * 这笔成交发生在哪个 PF 账户里。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：成交是<strong>盘面上的事实</strong>，
     * 不是账户的从属物。删账户不删成交，这里仍然保留原来那个 id（不置空）。
     * 创建时必须有账户，见 {@link #fill}。
     */
    @Column(nullable = false)
    private String accountId;

    /**
     * 成交发生在哪个品种上。
     *
     * <p>一个 PF 账户可以同时做 MES / MNQ / MGC，所以品种不能从账户推 ——
     * 必须记在成交自己身上，否则「这笔成交是哪张图上的」无从回答。
     *
     * <p>NOT NULL 且带外键（指向 {@code symbol.id}）：标的是一张长期存在的表，
     * 不会因为删账户而消失，这里没有"保留原 id 供审计"的需求。
     */
    @Column(nullable = false)
    private String symbolId;

    /** 触发这张成交的挂单。市价单也有，就是那张一次性存在的 MARKET 单。 */
    @Column(nullable = false)
    private String orderId;

    /** 成交所在 K 线的时间明文。 */
    @Column(nullable = false)
    private String barTime;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Side side;

    @Column(nullable = false)
    private BigDecimal price;

    @Column(nullable = false)
    private BigDecimal quantity;

    /**
     * 这一笔平掉的部分实现的盈亏（美元），**平均成本法**算出来的；开仓/加仓为 0。
     *
     * <p>它是记账口径，不是归因口径。要「每一批仓位各自赚了多少」得看
     * {@code TradeLedger#closedLots} 的结果 —— 两者在「一次平掉全部持仓」时相等，
     * 在「只平一部分」时按定义就不同（平均成本法 vs FIFO 逐批成本）。
     */
    @Column(nullable = false)
    private BigDecimal realizedPnl;

    /**
     * 这一笔成交的<strong>单边手续费</strong>（美元），成交那一刻的费率快照。
     *
     * <p>为什么不"读 {@code symbol.commission} 现算"：手续费率和考核规则一样，
     * 是<strong>签下那一刻的事实</strong>。用户半年后把 MES 的费率从 0.5 改成 0.6，
     * 上个月的净盈亏不能跟着变 —— 变了就意味着历史考核成绩可以被今天的配置改写。
     *
     * <p>开仓和平仓<strong>都收</strong>：一笔往返等于两次本字段。这就是
     * {@code Symbol#roundTripCommission} 不被这里使用的原因 —— 那边是"预估成本"，
     * 这里是"已经发生的成本"。
     */
    @Column(nullable = false)
    private BigDecimal commission;

    /**
     * 这笔成交属于哪一条交易记录；NULL = 没被任何记录收进去。
     *
     * <p>它是<strong>弱引用</strong>：交易记录被删掉不影响成交本身，
     * 成交是盘面上的事实，记录只是对这一笔的解释。
     */
    @Setter
    private String tradeRecordId;

    /**
     * 记一笔成交。
     *
     * @param accountId  在哪个 PF 账户里成交，<strong>不能为空</strong>：成交只可能发生在
     *                   某个考核期里。允许为空的列只是为「账户被删」留的出口
     * @param symbolId   在哪个品种上成交，<strong>不能为空</strong>：账户不再绑定单个品种
     * @param commission 这一笔的单边手续费，<strong>不能为空</strong>：没配手续费就不该有成交，
     *                   否则净盈亏算出来是错的还看不出来
     * @throws IllegalArgumentException 没给账户 / 标的 / 手续费
     */
    public static Trade fill(String accountId, String symbolId, String orderId, String barTime, Side side,
                             BigDecimal price, BigDecimal quantity, BigDecimal realizedPnl,
                             BigDecimal commission) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("成交必须发生在某个 PF 账户里，收到: " + accountId);
        }
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("成交必须发生在某个标的上，收到: " + symbolId);
        }
        if (commission == null) {
            throw new IllegalArgumentException("成交必须带手续费（可以为 0，但不能没有）");
        }
        Trade trade = new Trade();
        trade.accountId = accountId;
        trade.symbolId = symbolId;
        trade.orderId = orderId;
        trade.barTime = barTime;
        trade.side = side;
        trade.price = price;
        trade.quantity = quantity;
        trade.realizedPnl = realizedPnl;
        trade.commission = commission;
        return trade;
    }

    /**
     * 这笔成交的<strong>净</strong>盈亏 = 已实现毛利 − 手续费。
     *
     * <p>开仓那一笔的 {@code realizedPnl} 是 0，所以它返回的是<strong>负数</strong>
     * （只扣了进场费）—— 这是对的，钱确实在开仓时就已经付了。
     * 账户余额、一致性、盈利日全部用这个口径，画平仓虚线用 {@code realizedPnl}。
     */
    public BigDecimal netPnl() {
        return realizedPnl.subtract(commission);
    }

    /** 带符号的数量：买为正、卖为负。持仓计算全靠它。 */
    public BigDecimal signedQuantity() {
        return side == Side.BUY ? quantity : quantity.negate();
    }
}
