package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.OrderView;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 改了挂单价（画布上拖动挂单线，{@link ReplayAction#ORDER_MOVED}）。对应 Go 的
 * {@code OrderMovedPayload}。
 *
 * <p>比 {@link OrderPlacedPayload} 多带一个 {@code order}：操作记录那一行要写清
 * 「改的是哪一张、改成了多少」。只给全量列表的话，前端只能靠猜 —— 列表里没有
 * 「刚改的那张」的标记。
 */
public record OrderMovedPayload(
        OrderView order,
        List<OrderView> orders
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_MOVED;
    }
}
