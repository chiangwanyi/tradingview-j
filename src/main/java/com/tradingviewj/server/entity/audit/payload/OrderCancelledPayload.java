package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 撤了一张单（{@link ReplayAction#ORDER_CANCELLED}）。对应 Go 的 {@code OrdersPayload}。
 *
 * <p>和 {@link OrderPlacedPayload} 字段完全相同：撤单只改挂单区的成员，不动成交、持仓和盈亏 ——
 * 一张没成交的单消失了，账面上什么都没发生。
 *
 * <p>撤单<strong>不是</strong>平仓。平仓一定会产生一笔成交（见 {@link OrderFilledPayload}），
 * 两者在操作记录里必须能分开，否则「他到底平没平掉」这件事就查不清了。
 */
public record OrderCancelledPayload(
        List<Order> orders
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_CANCELLED;
    }
}
