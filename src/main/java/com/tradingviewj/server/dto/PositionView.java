package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.trade.Position;

import java.math.BigDecimal;

/**
 * 净头寸在协议里的样子。
 *
 * <p>{@code quantity} 带符号（正 = 多、负 = 空、0 = 空仓），{@code side} 是从符号推出来的
 * 展示值（{@code long} / {@code short} / {@code flat}）。两个字段冗余是刻意的：
 * 画布要按 {@code side} 选颜色，账户面板要按 {@code quantity} 算手数，
 * 让它们各自去判一次符号，迟早有一处把「负数量」当成「卖」。
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
                position.averagePrice());
    }
}
