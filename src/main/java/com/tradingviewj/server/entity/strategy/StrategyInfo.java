package com.tradingviewj.server.entity.strategy;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「有一个什么策略可以跑」（对应 Go 的 {@code strategy.Info}）。
 *
 * <p>只发描述，不发实现：前端不需要知道策略内部怎么判断，它只负责把 id 回传过来启动。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public class StrategyInfo {

    private String id;

    private String name;

    private String summary;

    private List<StrategyParam> params;
}
