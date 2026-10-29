package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 【市价退仓并取消所有挂单】的负载（对应 Go 的 {@code replay.FlatAllPayload}）。
 *
 * <p>一次动作可能做两件事：撤掉全部挂单、按现价把整个仓位平掉。所以它既不是
 * {@link OrdersPayload}（还带着成交和持仓）也不是 {@link OrderFilledPayload}（本来空仓时没有成交）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class FlatAllPayload {

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Order order;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Trade trade;

    /** 撤销了几张挂单 */
    private int cancelled;

    private List<Order> orders;

    private List<Trade> trades;

    private List<ClosedLot> closedLots;

    private Position position;

    private double realizedPnl;

    private double unrealizedPnl;
}
