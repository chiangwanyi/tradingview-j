package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 选标的时的一项（对应 Go 的 {@code replay.SymbolInfo}），来自数据目录、不读文件内容。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class SymbolInfo {

    private String symbol;

    private String folder;

    private int days;

    private String firstDate;

    private String lastDate;

    /** 最小跳动，前端用它决定价格轴与提示的小数位 */
    private double tick;

    /** 一个 tick 值多少美元；0 = 没配置，结算按每点 1 美元 */
    private double tickValue;
}
