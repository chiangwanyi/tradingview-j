package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.OrderView;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 挂了一张单（{@link ReplayAction#ORDER_PLACED}）。对应 Go 的 {@code OrdersPayload}。
 *
 * <p>带的是<strong>全量挂单列表</strong>而不是新增的那一张：一次「挂单」可能同时产生好几张
 * （机器人开仓时会顺手挂止损 + 止盈），而前端的挂单区本来就是整份替换 —— 发全量
 * 就没有「哪几张是新的」这种歧义。
 *
 * <p>{@code ORDER_CANCELLED} 用的是同一个形状，见 {@link OrderCancelledPayload}。
 *
 * <p>列表里是 {@link OrderView}（协议形状）而不是 {@code Order} 实体：这份负载会原样
 * 序列化进 {@code audit_event.payload_json} 并广播出去，<strong>两者必须是同一份</strong>。
 * 放实体进去，库里和协议里就都会出现 {@code "side":"BUY"} —— 前端要么处处大写比较、
 * 要么自己转一次，而换算一旦有两处，改协议时就会漏掉一处。
 */
public record OrderPlacedPayload(
        List<OrderView> orders
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_PLACED;
    }
}
