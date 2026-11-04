package com.tradingviewj.server.entity.trade;

/**
 * 买卖方向。
 *
 * <p>挂单和成交共用同一个枚举（Go 版是 {@code OrderSide} 和 {@code TradeSide} 两份，
 * 取值一模一样）。两个取值完全相同的枚举只会带来「转来转去」的代码，
 * 而且迟早有一边新增了取值另一边没跟上。
 */
public enum Side {
    BUY, SELL;

    /** 反方向。平仓单靠它算，省得调用方到处写三元表达式。 */
    public Side opposite() {
        return this == BUY ? SELL : BUY;
    }
}
