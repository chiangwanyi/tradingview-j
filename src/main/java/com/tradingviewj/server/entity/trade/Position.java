package com.tradingviewj.server.entity.trade;

import java.math.BigDecimal;

/**
 * 净头寸：带符号的数量 + 平均成本。<strong>值对象，不落库</strong>。
 *
 * <p>它是成交流水的<strong>派生结果</strong>，由 {@code service.TradeLedger} 每次现算。
 * 之所以不落库：存一份副本就一定会有一天和成交对不上，而当两者不一致时，
 * 没有任何办法判断谁是对的 —— 成交是事实，持仓是解释，事实比解释可靠。
 *
 * <p>{@code quantity} 带符号：正 = 多，负 = 空，0 = 空仓。
 * 空头的浮盈是 {@code (close - averagePrice) * quantity}，符号天然正确，不用再判方向。
 */
public record Position(BigDecimal quantity, BigDecimal averagePrice) {

    public static final Position FLAT = new Position(BigDecimal.ZERO, BigDecimal.ZERO);

    /**
     * 持仓方向。
     *
     * <p>空仓返回 {@code null}。没有为「多 / 空 / 平」再造一个 {@code PositionSide} 枚举：
     * 那会是整个世界里的第三个方向枚举（前两个是 {@link Side} 和订单状态），
     * 三者互相映射只会制造一堆转换代码。调用方拿 {@link #isFlat()} 判空仓更直接。
     */
    public Side side() {
        int sign = quantity.signum();
        if (sign > 0) {
            return Side.BUY;
        }
        return sign < 0 ? Side.SELL : null;
    }

    public boolean isFlat() {
        return quantity.signum() == 0;
    }

    /**
     * 浮盈（美元）：按给定价格算。空仓返回 0。
     *
     * <p>过 {@link Money#round} 是必须的：{@code averagePrice} 可能是除不尽的循环小数
     * （见 {@code TradeLedger} 的均价除法），不收敛就会让「余额 = 起点 + 已实现 + 浮动」
     * 这个等式带上无穷尾数，而 PF 判定里用的是<strong>等于即失败</strong>的比较。
     */
    public BigDecimal unrealizedPnl(BigDecimal price, BigDecimal pointValue) {
        if (isFlat()) {
            return BigDecimal.ZERO;
        }
        return Money.round(price.subtract(averagePrice).multiply(quantity).multiply(pointValue));
    }
}
