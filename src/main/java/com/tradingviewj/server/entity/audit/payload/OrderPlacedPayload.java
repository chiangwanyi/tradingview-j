package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 挂了一张单（{@link ReplayAction#ORDER_PLACED}）。对应 Go 的 {@code OrdersPayload}。
 *
 * <p>带的是<strong>全量挂单列表</strong>而不是新增的那一张：一次「挂单」可能同时产生好几张
 * （策略机器人开仓时会顺手挂止损 + 止盈），而前端的挂单区本来就是整份替换 —— 发全量
 * 就没有「哪几张是新的」这种歧义。
 *
 * <p>{@code ORDER_CANCELLED} 用的是同一个形状，见 {@link OrderCancelledPayload}。
 */
public record OrderPlacedPayload(
        List<Order> orders
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_PLACED;
    }
}
