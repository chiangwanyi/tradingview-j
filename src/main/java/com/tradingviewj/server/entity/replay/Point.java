package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 标注的一个端点（对应 Go 的 {@code replay.Point}）：横坐标是「一根已揭示 K 线的时间 + 向右延伸的根数」。
 *
 * <p>time 是锚点，必须是已揭示数据里某一根 K 线的时间——指向还没出现的 K 线等于泄露后面的行情。
 * offset 是从锚点往右再走多少根，按根数延伸而不引用任何未来时间：未来的时间遇到周末/休市就会错位，
 * 且新 K 线揭示后同一个未来时间会落到另一个下标上，画好的线会自己左右跳。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Point {

    private String time;

    private double price;

    /** 向右延伸的 K 线根数，&gt;= 0；为 0 时不出现在 JSON 里（旧存档/旧客户端照旧） */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private int offset;
}
