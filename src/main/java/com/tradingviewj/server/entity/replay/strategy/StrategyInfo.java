package com.tradingviewj.server.entity.replay.strategy;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「有一个什么策略可以跑」的协议形态（对应 Go 的 {@code replay.StrategyInfo}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class StrategyInfo {

    private String id;

    private String name;

    private String summary;

    private List<StrategyParam> params;
}
