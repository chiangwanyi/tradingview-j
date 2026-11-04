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
 * 一张挂单。
 *
 * <p><strong>没有「止损单 / 止盈单」这种类型</strong>：想止损就自己挂一张 {@link Type#STOP}，
 * 想止盈就挂 {@link Type#LIMIT}。这不只是少一个字段 —— 一旦有了「止损单」类型，
 * 就得定义「止损单能不能改价」「改了还是不是止损单」，规则会一直长下去。
 *
 * <p>{@link Type#MARKET} 是唯一的例外：它只在「市价成交」那一刻存在，落库时
 * {@link #status} 已经是 {@link Status#FILLED}。<strong>但它和挂单走同一条状态机</strong>：
 * {@link #place} 出来同样是 {@link Status#OPEN}，紧接着由下单用例调
 * {@link #markFilled} 变成 FILLED —— 市价单不该有另一条「建出来就是已成交」的路。
 * 留着这个类型是因为成交、历史、撤单列表要共用同一套数据结构，多一张表不如多一个取值。
 *
 * <p><strong>没有 OCO 组</strong>（2026-10-31 删 {@code ocoGroup}）：`webapp` 只支持
 * 独立挂单，同根双触发无人能用得上，留着字段就得连带养一套撮合裁决与测试。
 * 一张单和别的单之间<strong>没有</strong>任何组合关系 —— 要「同根只成交一张」，
 * 现在的答案是撤掉另一张，而不是给两张单打组。
 */
@Getter
@Entity
@Table(name = "trade_order")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class TradeOrder extends BaseEntity {

    /**
     * 挂在哪个 PF 账户的盘面上。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：账户被删掉之后这里仍然保留原来那个 id，
     * 挂单本身原样保留。创建时必须有账户，见 {@link #place}。
     */
    @Column(nullable = false)
    private String accountId;

    /** 挂在哪个品种上。账户可以同时做多个品种，所以挂单必须自己记着品种。 */
    @Column(nullable = false)
    private String symbolId;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Side side;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Type type;

    @Setter
    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Status status;

    /** 挂单价。市价单这里记「下单那一刻的收盘价」，只用于显示。 */
    @Setter
    @Column(nullable = false)
    private BigDecimal price;

    @Column(nullable = false)
    private BigDecimal quantity;

    /**
     * 下单那一刻的 K 线时间明文。
     *
     * <p><strong>这是「哪一根」的唯一答案</strong>，没有配套的下标。曾经有 {@code placed_index}
     * 和 {@code filled_index}，2026-10-29 删了：下标是<strong>位置</strong>，
     * 数据文件被重新抓取、补一根、删一根坏行，同一个下标就指向另一根 K 线，而且不报错；
     * 时间不会。另外它只在某个标的下成立，而账户不绑标的。
     */
    @Column(nullable = false)
    private String placedAtBar;

    /**
     * 成交那一刻的 K 线时间明文；{@code null} = 没成交。
     *
     * <p>「没成交」由这一列的空值表达，不再需要 {@code filled_index = -1} 那种哨兵值 ——
     * 一个含义配一个表示法就够了。
     */
    @Setter
    private String filledAtBar;

    /** 实际成交价。跳空时可能不等于挂单价（开盘直接跳过挂单价就取开盘价）。 */
    @Setter
    private BigDecimal fillPrice;

    /**
     * 挂一张单。
     *
     * @param accountId 在哪个 PF 账户里下单，<strong>不能为空</strong>；账户是不是还能交易
     *                  （ACTIVE 且不在 PAYOUT）由 {@code service.PropFirmAccountService#requireTradable} 把关
     * @param symbolId  挂在哪个品种上，<strong>不能为空</strong>
     * @param placedAtBar 下单那一刻停在哪根 K 线上，<strong>不能为空</strong>
     * @throws IllegalArgumentException 没给账户 / 标的 / K 线时间
     */
    public static TradeOrder place(String accountId, String symbolId, Side side, Type type, BigDecimal price,
                                   BigDecimal quantity, String placedAtBar) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("下单必须发生在某个 PF 账户里，收到: " + accountId);
        }
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("下单必须指定标的，收到: " + symbolId);
        }
        if (placedAtBar == null || placedAtBar.isBlank()) {
            throw new IllegalArgumentException("下单必须记着停在哪根 K 线上，收到: " + placedAtBar);
        }
        TradeOrder order = new TradeOrder();
        order.accountId = accountId;
        order.symbolId = symbolId;
        order.side = side;
        order.type = type;
        order.price = price;
        order.quantity = quantity;
        order.placedAtBar = placedAtBar;
        // 一律从 OPEN 开始，市价单也不例外 —— 它在下单的同一步里立刻 markFilled。
        // 不能在这里直接置 FILLED：那样 markFilled 会因为「已经不是 OPEN」而抛异常，
        // 市价单的成交价和成交时间就只能靠调用方直接调 setter 硬填，状态机凭空多一条路。
        order.status = Status.OPEN;
        return order;
    }

    /**
     * 记成交。一张单只能成交一次 —— 这是领域规则，写在实体里而不是指望调用方自觉。
     *
     * <p>市价单走的是同一条路：{@link #place} 出来是 {@code OPEN}，紧接着就调这里。
     * 所以「一张单怎么变成已成交」只有一种写法。
     *
     * @throws IllegalStateException 已经成交 / 已经撤销的单再记成交
     */
    public void markFilled(String barTime, BigDecimal fillPrice) {
        if (status != Status.OPEN) {
            throw new IllegalStateException("挂单 " + getId() + " 当前是 " + status + "，不能成交");
        }
        this.status = Status.FILLED;
        this.filledAtBar = barTime;
        this.fillPrice = fillPrice;
    }

    /** 撤单。只有还挂着的单能撤，已成交的撤不掉（那是成交，不是撤销）。 */
    public void cancel() {
        if (status == Status.OPEN) {
            this.status = Status.CANCELLED;
        }
    }

    public boolean isOpen() {
        return status == Status.OPEN;
    }

    public enum Type {
        /** 限价单：价格到挂单价才成交。 */
        LIMIT,
        /** 止损单：价格穿过挂单价才成交。 */
        STOP,
        /** 市价单：下单即按当前收盘价成交，不会留在挂单列表里。 */
        MARKET
    }

    public enum Status {
        OPEN, FILLED, CANCELLED
    }
}
