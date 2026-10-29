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
 * {@link #status} 已经是 {@link Status#FILLED}。留着它是因为成交、历史、撤单列表
 * 要共用同一套数据结构，多一张表不如多一个取值。
 *
 * <p>{@link #ocoGroup} 是 OCO 组：同一组里最多成交一张，其余在本根自动撤销。
 * 机器人开仓时给止损和止盈打同一组 —— 5 分钟 K 线看不出高低点谁先出现，
 * 不打组的话同根双触发会把仓位平两遍，凭空多出一个反向仓。
 */
@Getter
@Entity
@Table(name = "trade_order")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Order extends BaseEntity {

    /**
     * 挂在哪个 PF 账户的盘面上；NULL = 所在账户已被删除（挂单本身保留）。
     *
     * <p>创建时必须有账户，见 {@link #place}。
     */
    @Column
    private String accountId;

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

    /** 下单那一刻的 K 线时间明文。 */
    @Column(nullable = false)
    private String placedAtBar;

    /** 下单那一刻的 K 线下标。 */
    @Column(nullable = false)
    private int placedIndex;

    @Setter
    private String filledAtBar;

    /** 成交时的 K 线下标；-1 = 没成交。 */
    @Setter
    @Column(nullable = false)
    private int filledIndex = -1;

    /** 实际成交价。跳空时可能不等于挂单价（开盘直接跳过挂单价就取开盘价）。 */
    @Setter
    private BigDecimal fillPrice;

    /** OCO 组号；NULL = 独立挂单。 */
    @Column
    private String ocoGroup;

    /**
     * 挂一张单。
     *
     * @param accountId 在哪个 PF 账户里下单，<strong>不能为空</strong>；账户是不是还能交易
     *                  （ACTIVE）由 {@code service.PropFirmAccountService#requireActive} 把关
     * @throws IllegalArgumentException 没给账户
     */
    public static Order place(String accountId, Side side, Type type, BigDecimal price,
                              BigDecimal quantity, String placedAtBar, int placedIndex, String ocoGroup) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("下单必须发生在某个 PF 账户里，收到: " + accountId);
        }
        Order order = new Order();
        order.accountId = accountId;
        order.side = side;
        order.type = type;
        order.price = price;
        order.quantity = quantity;
        order.placedAtBar = placedAtBar;
        order.placedIndex = placedIndex;
        order.ocoGroup = ocoGroup;
        order.status = type == Type.MARKET ? Status.FILLED : Status.OPEN;
        return order;
    }

    /**
     * 记成交。一张单只能成交一次 —— 这是领域规则，写在实体里而不是指望调用方自觉。
     *
     * @throws IllegalStateException 已经成交 / 已经撤销的单再记成交
     */
    public void markFilled(String barTime, int barIndex, BigDecimal fillPrice) {
        if (status != Status.OPEN) {
            throw new IllegalStateException("挂单 " + getId() + " 当前是 " + status + "，不能成交");
        }
        this.status = Status.FILLED;
        this.filledAtBar = barTime;
        this.filledIndex = barIndex;
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
