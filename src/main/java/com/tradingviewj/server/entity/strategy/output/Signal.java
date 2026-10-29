package com.tradingviewj.server.entity.strategy.output;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 策略给出的一张单（对应 Go 的 {@code strategy.Signal}）。
 *
 * <p>策略保证 stop 在正确的一侧（多头在下方、空头在上方），所以风险恒为正。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public final class Signal {

    /** buy | sell */
    private String side;

    /** 止损触发价 */
    private double stop;

    /** 止盈价 */
    private double target;

    /** 一句话说明，写进日志用 */
    private String reason;
}
