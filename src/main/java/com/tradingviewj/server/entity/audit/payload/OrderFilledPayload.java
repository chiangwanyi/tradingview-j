package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.ReplayAction;

import java.math.BigDecimal;
import java.util.List;

/**
 * 一张单成交了（{@link ReplayAction#ORDER_FILLED}）。对应 Go 的 {@code OrderFilledPayload}。
 *
 * <p>成交会<strong>立刻</strong>改持仓、成交列表和盈亏，所以这里一次把这些全部发全量：
 * 前端不用再要一次快照，也不会出现「成交了但持仓没变」的中间态。
 *
 * <p>{@code stopOrder} 是这一笔<strong>顺手挂上</strong>的保护性止损（按 nATR 定手数时才有），
 * 可以为 {@code null}。它同时也出现在 {@code orders} 里（全量列表），单独给一份是为了
 * 「操作记录那一行」：只写「买市价 5 手」看不出还挂了止损，而复盘时最该一眼看到的
 * 恰恰是「这一笔是有保护的」。
 */
public record OrderFilledPayload(
        Order order,
        Trade trade,
        Order stopOrder,
        List<Order> orders,
        List<Trade> trades,
        List<ClosedLot> closedLots,
        Position position,
        BigDecimal realizedPnl,
        BigDecimal unrealizedPnl
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.ORDER_FILLED;
    }
}
