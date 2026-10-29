package com.tradingviewj.server.entity.replay.strategy;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个策略参数的协议形态（对应 Go 的 {@code replay.StrategyParam}），前端启动面板按它列出旋钮。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class StrategyParam {

    private String key;

    private String label;

    private double defaultValue;

    private double min;

    private double max;

    private double step;

    private String help;
}
