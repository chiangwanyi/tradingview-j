package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「这个星期几应当有多少根 K 线」的一行（对应 Go 的 {@code store.WeekExpect}）。
 *
 * <p>bars 会给出两个取值：美国夏令时/冬令时会把 CME 的结算时刻在本地整体挪 1 小时。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class WeekExpect {

    /** 周一…周日 */
    private String weekday;

    /** 正常取值，可能是两个（夏令时/冬令时） */
    private List<Integer> bars;
}
