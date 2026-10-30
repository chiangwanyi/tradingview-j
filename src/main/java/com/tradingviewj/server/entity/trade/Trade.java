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
import java.util.Comparator;

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

    /**
     * 成交流水号：同一账户内从 1 起单调递增。
     *
     * <p><strong>排序只认它。</strong> 同一根 K 线上可以有多笔成交（止损平仓 + 反手开仓、
     * 两个品种同一时刻各成交一笔），这时按 {@code barTime} 排是分不出先后的，
     * 而按主键（UUID）排等于随机排 —— 均价和已实现盈亏会在反手、部分平仓这些边界上算错，
     * 而且每次重启错得还不一样，是最难查的一类 bug。
     *
     * <p>由写入方在同一事务里取 {@code max(seq) + 1}（单账户单写者，不需要锁）。
     * 它不是数据库自增列：复盘里的成交是「先有对象再落库」，seq 在落库前就要用来排序。
     */
    @Column(nullable = false, updatable = false)
    private int seq;

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
     * @param seq        成交流水号，<strong>必须为正</strong>：同一账户内单调递增，
     *                   见 {@link #seq}。它不是可选装饰 —— 没有它就没法确定同一根 K 线上
     *                   多笔成交的先后
     * @param commission 这一笔的单边手续费，<strong>不能为空</strong>：没配手续费就不该有成交，
     *                   否则净盈亏算出来是错的还看不出来
     * @throws IllegalArgumentException 没给账户 / 标的 / 手续费，或者 seq 不是正数
     */
    public static Trade fill(String accountId, String symbolId, String orderId, String barTime, int seq,
                             Side side, BigDecimal price, BigDecimal quantity, BigDecimal realizedPnl,
                             BigDecimal commission) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("成交必须发生在某个 PF 账户里，收到: " + accountId);
        }
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("成交必须发生在某个标的上，收到: " + symbolId);
        }
        if (seq <= 0) {
            throw new IllegalArgumentException("成交流水号必须从 1 开始，收到: " + seq);
        }
        if (commission == null) {
            throw new IllegalArgumentException("成交必须带手续费（可以为 0，但不能没有）");
        }
        if (realizedPnl == null) {
            throw new IllegalArgumentException("成交必须带已实现盈亏（开仓写 0，不能没有）");
        }
        Trade trade = new Trade();
        trade.accountId = accountId;
        trade.symbolId = symbolId;
        trade.orderId = orderId;
        trade.barTime = barTime;
        trade.seq = seq;
        trade.side = side;
        trade.price = price;
        trade.quantity = quantity;
        // 金额在这里收敛到分，是**落库前的最后一道闸**：调用方（recordFill）已经算过一遍，
        // 但成交是唯一真相，宁可多舍一次也不能让 834.99996 这种尾数进库 —— 进去之后
        // 余额、考核、验证、导出全都跟着脏，而且无从判断哪些是有意为之。
        trade.realizedPnl = Money.round(realizedPnl);
        trade.commission = Money.round(commission);
        return trade;
    }

    /**
     * 成交的正确先后顺序：先按 K 线时间，同一根再按 {@link #seq}。
     *
     * <p>放在这里是为了让「重放前怎么排」只有一个答案。{@code TradeLedger.replay} 的入参
     * 要求按顺序排好，调用方各自写一个 comparator 迟早有人写成按 id 排。
     */
    public static Comparator<Trade> executionOrder() {
        return Comparator.comparing(Trade::getBarTime).thenComparingInt(Trade::getSeq);
    }

    /**
     * 这笔成交的<strong>净</strong>盈亏 = 已实现毛利 − 手续费。
     *
     * <p>开仓那一笔的 {@code realizedPnl} 是 0，所以它返回的是<strong>负数</strong>
     * （只扣了进场费）—— 这是对的，钱确实在开仓时就已经付了。
     * 账户余额、一致性、盈利日全部用这个口径，画平仓虚线用 {@code realizedPnl}。
     *
     * <p>再收敛一次 {@link Money#round}：{@code realizedPnl} 已经是分，但手续费是
     * 「费率 × 手数」，手数有四位小数时可能算出亚分的数（0.5 × 0.3333 = 0.16665），
     * 不归整就会让净额把尾数带回余额里。
     */
    public BigDecimal netPnl() {
        return Money.round(realizedPnl.subtract(commission));
    }

    /** 带符号的数量：买为正、卖为负。持仓计算全靠它。 */
    public BigDecimal signedQuantity() {
        return side == Side.BUY ? quantity : quantity.negate();
    }
}
