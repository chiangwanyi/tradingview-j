package com.tradingviewj.server.entity.strategy;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个策略参数的描述（对应 Go 的 {@code strategy.Param}），前端启动面板按它列出旋钮。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public class StrategyParam {

    private String key;

    private String label;

    private double defaultValue;

    private double min;

    private double max;

    private double step;

    /** 给用户看的说明，界面上做 tooltip */
    private String help;
}
