package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 某一天所有标的的合计（对应 Go 的 {@code store.DateTotal}，也是网页日历的数据源）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class DateTotal {

    private String date;

    private int symbols;

    private int bars;

    /** 这一天有几个标的的文件带异常（根数不符 / 坏行 / 重复行 / 读不了） */
    private int issues;

    /** 这一天的根数在各标的之间不完全一致 */
    private boolean mismatch;

    /** 出现最多的那个根数（不一致时用它挑出「少数派」） */
    private int majority;

    /** 各种根数各有几个标的 */
    private List<BarCount> counts;
}
