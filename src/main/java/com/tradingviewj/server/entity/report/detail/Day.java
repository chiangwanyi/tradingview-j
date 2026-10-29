package com.tradingviewj.server.entity.report.detail;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.store.scan.Gap;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 网页上的「一个日文件」（对应 Go 的 {@code webreport.Day}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class Day {

    private String date;

    /** 解析出来的根数（同一时间戳只算一根） */
    private int bars;

    /** 文件里的数据行数（不含表头） */
    private int rows;

    private int skipped;

    private int dupes;

    private long firstT;

    private long lastT;

    private long bytes;

    /** 时间断档，按时间递增 */
    private List<Gap> gaps;

    /** 这一天按星期几应当有多少根 */
    private List<Integer> expect;

    /** 根数不在 expect 里 */
    private boolean barsOff;

    /** 同一天各标的的根数不完全相同 */
    private boolean mismatch;

    /** 异常（根数不符 / 坏行 / 重复行 / 读不了），判定在 store 里 */
    private boolean anomaly;

    /** 非空 = 这份文件没统计到 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String err;
}
