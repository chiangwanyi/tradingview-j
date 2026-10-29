package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 挂单列表全量（对应 Go 的 {@code replay.OrdersPayload}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class OrdersPayload {

    private List<Order> orders;
}
