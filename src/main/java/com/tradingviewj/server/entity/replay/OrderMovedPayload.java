package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 改挂单价的负载（对应 Go 的 {@code replay.OrderMovedPayload}）。
 *
 * <p>比 {@link OrdersPayload} 多带一个 order：操作记录里要写清「改的是哪一张、改成了多少」，
 * 只给全量列表的话前端只能靠猜。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class OrderMovedPayload {

    private Order order;

    private List<Order> orders;
}
