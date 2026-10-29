package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 策略清单（对应 Go 的 {@code replay.StrategiesMsg}）。
 *
 * <p>策略是编译期注册的、运行期不会变，但每次请求都发全量——前端连上就要一次，没必要做缓存失效。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class StrategiesMsg {

    private String type;

    private List<StrategyInfo> strategies;
}
