package com.tradingviewj.server.entity.strategy.input;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 策略看到的持仓（对应 Go 的 {@code strategy.Position}）。
 *
 * <p>quantity 带符号：正 = 多，负 = 空，0 = 空仓。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public final class StrategyPosition {

    private double quantity;

    private double averagePrice;
}
