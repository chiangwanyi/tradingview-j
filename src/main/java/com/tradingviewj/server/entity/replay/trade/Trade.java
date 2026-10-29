package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一笔成交（对应 Go 的 {@code replay.Trade}）= 一次挂单被触发。
 *
 * <p>不做手续费/滑点；成交价通常等于挂单价。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class Trade {

    private String id;

    /** 由哪张挂单触发（市价单也有，就是那张一次性存在的 market 单） */
    private String orderId;

    /** 成交所在 K 线的时间 */
    private String time;

    private OrderSide side;

    private OrderKind kind;

    private double price;

    private double quantity;

    /** 这一笔平掉的部分实现的盈亏；开仓/加仓为 0 */
    private double realizedPnl;
}
