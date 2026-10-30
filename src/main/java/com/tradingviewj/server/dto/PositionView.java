package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.trade.Money;
import com.tradingviewj.server.entity.trade.Position;

import java.math.BigDecimal;

/**
 * 净头寸在协议里的样子。
 *
 * <p>{@code quantity} 带符号（正 = 多、负 = 空、0 = 空仓），{@code side} 是从符号推出来的
 * 展示值（{@code long} / {@code short} / {@code flat}）。两个字段冗余是刻意的：
 * 画布要按 {@code side} 选颜色，账户面板要按 {@code quantity} 算手数，
 * 让它们各自去判一次符号，迟早有一处把「负数量」当成「卖」。
 *
 * <p><strong>均价在这里收敛到分，纯粹为了少传几个字节。</strong> 它是「总成本 / 总手数」，
 * 除不尽时是循环小数（六笔一手的均价 = 29097.6666…，34 位精度会原样序列化成
 * {@code 29097.66666666666666666666666666667}）。这个数会进 WS 帧和 {@code audit_event} 的
 * payload，越短越好读。
 *
 * <p>⚠️ <strong>它只用于展示，绝不能回流计算</strong>：把均价舍到 29097.67 再乘回手数，
 * 就会重新造出「835 变成 834.96」那类偏差。算账用的 {@link Position} 一直保持全精度，
 * 浮盈在 {@code Position#unrealizedPnl} 里算完才收敛，跟这里无关。
 */
public record PositionView(BigDecimal quantity, String side, BigDecimal averagePrice) {

    public static final PositionView FLAT = new PositionView(BigDecimal.ZERO, "flat", BigDecimal.ZERO);

    public static PositionView of(Position position) {
        if (position == null || position.isFlat()) {
            return FLAT;
        }
        return new PositionView(
                position.quantity(),
                position.quantity().signum() > 0 ? "long" : "short",
                Money.round(position.averagePrice()));
    }
}
