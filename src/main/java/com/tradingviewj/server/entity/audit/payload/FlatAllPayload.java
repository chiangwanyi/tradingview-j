package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.ClosedLotView;
import com.tradingviewj.server.dto.OrderView;
import com.tradingviewj.server.dto.PositionView;
import com.tradingviewj.server.dto.TradeView;
import com.tradingviewj.server.enums.ReplayAction;

import java.math.BigDecimal;
import java.util.List;

/**
 * 市价退仓 + 撤销全部挂单（{@link ReplayAction#FLAT_ALL}）。对应 Go 的 {@code FlatAllPayload}。
 *
 * <p>一次动作可能做两件事，所以它既不是 {@link OrderPlacedPayload}（那个不带成交和持仓），
 * 也不是 {@link OrderFilledPayload}（空仓时本来就没有成交）：{@code order} / {@code trade}
 * 是<strong>可空</strong>的，只在真有仓位时才有值。
 *
 * <p>{@code cancelled} 是撤掉了几张挂单。空仓 + 没挂单是唯一会被拒的情况 ——
 * 那种时候不广播（返回错误），否则静默成功会让用户以为「点了没反应」，
 * 而分不清是「没东西可平」还是「按钮坏了」。
 */
public record FlatAllPayload(
        OrderView order,
        TradeView trade,
        int cancelled,
        List<OrderView> orders,
        List<TradeView> trades,
        List<ClosedLotView> closedLots,
        PositionView position,
        BigDecimal realizedPnl,
        BigDecimal unrealizedPnl
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.FLAT_ALL;
    }
}
