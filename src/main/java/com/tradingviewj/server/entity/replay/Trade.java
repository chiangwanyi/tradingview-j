package com.tradingviewj.server.entity.replay;

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
public class Trade {

    private String id;

    private String orderId;

    private String time;

    private String side;

    private String kind;

    private double price;

    private double quantity;

    /** 这一笔平掉的部分实现的盈亏；开仓/加仓为 0 */
    private double realizedPnl;
}
