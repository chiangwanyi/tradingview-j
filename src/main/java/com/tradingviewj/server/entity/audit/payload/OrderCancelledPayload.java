package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.OrderView;
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
 *
 * <p>只记<strong>用户撤的单</strong>：撮合内部不会替用户撤任何单（没有 OCO、没有强平撤单），
 * 所以「一次动作一条广播」这条对应关系在撤单上是干净的。
 */
public record OrderCancelledPayload(
        List<OrderView> orders
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_CANCELLED;
    }
}
