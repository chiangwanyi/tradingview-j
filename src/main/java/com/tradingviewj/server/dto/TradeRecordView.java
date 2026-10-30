package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 交易记录列表里的一行（只有摘要，没有步骤 —— 这里只做查看）。
 *
 * <p>{@code realizedPnl} 是<strong>毛利</strong>（{@code trade_record.realized_pnl} 原值）；
 * 前端不再自己扣手续费。
 */
public record TradeRecordView(
        String id,
        String accountName,
        String symbolId,
        String side,
        BigDecimal quantity,
        String entryBarTime,
        String exitBarTime,
        BigDecimal entryPrice,
        BigDecimal exitPrice,
        BigDecimal realizedPnl,
        int barsHeld,
        boolean closed,
        String actor
) {
}
