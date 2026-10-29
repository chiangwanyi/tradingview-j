package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * {@code GET /api/timeframe} 的响应体（对应 Go 的 {@code replay.TimeframeBarsMsg}），HTTP 不是 WS。
 *
 * <p>故意比实例快照小得多：只有「已揭示的 K 线在这个周期下的样子」。
 * 订单、成交、标注、语音都不在里面——它们锚的是**时间**，不随周期变。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class TimeframeBarsMsg {

    private String instanceId;

    private String timeframe;

    private List<Bar> bars;
}
