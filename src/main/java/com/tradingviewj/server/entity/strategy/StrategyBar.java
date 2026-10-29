package com.tradingviewj.server.entity.strategy;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 喂给策略的一根 K 线（对应 Go 的 {@code strategy.Bar}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public class StrategyBar {

    /** "2026-10-16 21:30"，本地时区明文 */
    private String time;

    private double open;

    private double high;

    private double low;

    private double close;

    private double volume;
}
