package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 净头寸（对应 Go 的 {@code replay.Position}）：带符号的数量 + 平均成本。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Position {

    /** 正=多 负=空 0=空仓 */
    private double quantity;

    /** long | short | flat */
    private String side;

    private double averagePrice;
}
