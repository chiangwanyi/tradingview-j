package com.tradingviewj.server.entity.report;

import java.util.List;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.store.DateTotal;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「网页版分析」的整份快照（对应 Go 的 {@code webreport.Report}）。
 *
 * <p>这是菜单 2 的唯一报告出口的数据形态：只读 {@code store} 的扫描结果，不重算任何数字。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Report {

    private Meta meta;

    private Totals totals;

    /** 整份数据的时间跨度；一份数据都没有时为 null */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Span span;

    /** 扫描时的异常情况，原样透传 */
    private List<String> notes;

    private List<Folder> folders;

    /** 按日期聚合，日历的数据源 */
    private List<DateTotal> byDate;
}
