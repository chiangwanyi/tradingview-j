package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 机器人启动的负载（对应 Go 的 {@code replay.BotPayload}）。
 *
 * <p>strategy 为空表示老式的随机演示机器人；非空时是策略 id，name 是给人看的名字。
 * 前端靠这两个字段把「启动机器人」按钮改成「停止 Caveman」。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class BotPayload {

    private int intervalMs;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String strategy;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String name;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double quantity;
}
