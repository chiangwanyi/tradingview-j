package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Side;

import java.math.BigDecimal;

/**
 * 一批已经平掉的仓位（画布上一根平仓虚线的数据源）。
 *
 * <p>字段名和前端 {@code ClosedLot} 一致（{@code openTime} 而不是 {@code openBarTime}）。
 *
 * <p>{@code side} 用 {@code long} / {@code short} 而不是 {@code buy} / {@code sell}：
 * 它说的是<strong>被平掉的那个仓位方向</strong>，不是这一笔成交的方向。
 * 一笔「卖出平多」是 {@code short} 的仓位被平掉、由一张 SELL 成交完成 ——
 * 两个方向混用同一个词，画布的连线颜色就会在反手那一笔上画错。
 *
 * <p>{@link #realizedPnl} 是<strong>毛利</strong>（按这一批自己的开仓价算），
 * 不含手续费。手续费没有可靠的「逐批摊销」口径（一笔成交可能同时平掉几批），
 * 所以**不摊**：账户级的净额看 {@link TradeView#netPnl()} 的合计。
 */
public record ClosedLotView(
        String openTime,
        BigDecimal openPrice,
        String closeTime,
        BigDecimal closePrice,
        String side,
        BigDecimal quantity,
        BigDecimal realizedPnl
) {

    public static ClosedLotView of(ClosedLot lot) {
        return new ClosedLotView(
                lot.openBarTime(),
                lot.openPrice(),
                lot.closeBarTime(),
                lot.closePrice(),
                lot.side() == Side.BUY ? "long" : "short",
                lot.quantity(),
                lot.realizedPnl());
    }
}
