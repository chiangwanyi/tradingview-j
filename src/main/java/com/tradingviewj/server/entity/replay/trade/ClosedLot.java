package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一批已经平掉的仓位（对应 Go 的 {@code replay.ClosedLot}）。
 *
 * <p>与 {@link Trade} 的分工：Trade 是「一笔成交」，ClosedLot 是「一批仓位的完整生命周期」。
 * 一笔平仓成交可以同时平掉好几批，那就产出好几条——画布上每根平仓虚线对应其中一条。
 *
 * <p>盈亏用**这一批自己的开仓价**算，不用平均成本：那条线的两个端点就是这两个价，
 * 信息卡里也照这两个数显示，三者必须是同一个算法算出来的。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class ClosedLot {

    /** 开仓成交的 K 线时间（= 那个三角的横坐标） */
    private String openTime;

    /** 开仓的真实成交价（三角的纵坐标） */
    private double openPrice;

    private String closeTime;

    private double closePrice;

    /** long = 买开的仓，short = 卖开的仓 */
    private String side;

    /** 这一批被平掉的手数 */
    private double quantity;

    /** 这一批自己的已实现盈亏（美元） */
    private double realizedPnl;
}
