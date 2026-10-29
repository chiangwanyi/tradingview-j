package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 市价单成交的负载（对应 Go 的 {@code replay.OrderFilledPayload}）。
 *
 * <p>市价单会**立刻**改持仓、成交和盈亏，所以一次把全量都发出去：
 * 前端不用再要一次快照，也不会出现「成交了但持仓没变」的中间态。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class OrderFilledPayload {

    private Order order;

    private Trade trade;

    /**
     * 这一笔**顺手挂上的**保护性止损（nATR 定手数时才有）。
     *
     * <p>它同时出现在 orders 里（全量列表，前端整份替换），这个字段是给「操作记录那一行」用的：
     * 只写「买市价 5 手」看不出还挂了止损，而复盘时最该一眼看到的就是「这一笔是有保护的」。
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Order stopOrder;

    private List<Order> orders;

    private List<Trade> trades;

    private List<ClosedLot> closedLots;

    private Position position;

    private double realizedPnl;

    private double unrealizedPnl;
}
