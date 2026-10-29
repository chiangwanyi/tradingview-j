package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一张挂单（对应 Go 的 {@code replay.Order}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Order {

    private String id;

    /** buy | sell */
    private String side;

    /** limit | stop | market */
    private String kind;

    private double price;

    private double quantity;

    /** open | filled | cancelled */
    private String status;

    /** 下单那一刻的 K 线时间 */
    private String placedAt;

    /** 下单那一刻的 K 线下标 */
    private int placedIndex;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String filledAt;

    /** -1 = 没成交 */
    private int filledIndex;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double fillPrice;

    /**
     * OCO 组：同一组最多成交一张，其余在本根自动撤销；空 = 独立挂单。
     *
     * <p>机器人开仓时给止损和止盈打上同一组——5 分钟 K 线看不出高低点谁先出现，
     * 不打组的话同根双触发会把仓位平两遍，凭空多出一个反向仓。
     */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String group;
}
