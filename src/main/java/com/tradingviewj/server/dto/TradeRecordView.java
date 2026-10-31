package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 交易记录列表里的一行（只有摘要，没有步骤 —— 这里只做查看）。
 *
 * <p>{@code realizedPnl} 是<strong>毛利</strong>（{@code trade_record.realized_pnl} 原值）；
 * {@code commission} 是这条记录按腿切出来的手续费；{@code netPnl} = 前两者相减，
 * 由后端算好发出去 —— 前端不再自己扣手续费（前端一旦自己算就会出现两份口径）。
 *
 * <p>{@code netPnl} 在记录还没收尾时是 {@code null}：毛利要等仓位回到 0 才定，
 * 那时候给 0 会被读成「打平」。
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
        BigDecimal commission,
        BigDecimal netPnl,
        boolean closed,
        String actor
) {
}
