package com.tradingviewj.server.entity.strategy.input;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 策略每次被问到「该不该开仓」时拿到的全部输入（对应 Go 的 {@code strategy.Context}）。
 *
 * <p>bars 里只包含**已揭示**的 K 线，cursor 指向刚收盘的那根。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public final class StrategyContext {

    private List<StrategyBar> bars;

    private int cursor;

    /** 最小跳动，止损价的缓冲和过滤都用它 */
    private double tick;

    private StrategyPosition position;
}
