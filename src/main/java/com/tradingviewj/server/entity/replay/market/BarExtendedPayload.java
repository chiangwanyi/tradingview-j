package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 回放延伸步的负载（对应 Go 的 {@code replay.BarExtendedPayload}）。
 *
 * <p>**没有** orders / trades / position：延伸发生在回放末尾之后，那段时间没有订单、没有成交。
 * 硬凑一份持仓快照就是在编造当时的交易状态。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class BarExtendedPayload {

    private Bar bar;

    private int cursor;

    private String time;

    private boolean hasMore;
}
