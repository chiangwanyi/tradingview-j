package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个日文件的摘要（对应 Go 的 {@code store.DayStat}）。
 *
 * <p>「这一天不对劲」的判定（anomaly）在 Go 里是方法：err 非空、skipped &gt; 0、
 * dupes &gt; 0、barsOff 四者取或。前端不要另写一遍，口径只有一份。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class DayStat {

    /** 文件名里的日期 */
    private String date;

    private String path;

    /** 解析出来的根数（按时间戳去重后） */
    private int bars;

    /** 文件里的数据行数（不含表头） */
    private int rows;

    /** 无法解析、被跳过的行数 */
    private int skipped;

    /** 同一时间戳重复出现的行数（正常写入下恒为 0） */
    private int dupes;

    private long firstT;

    private long lastT;

    /** 首尾之间按 step 推算「本应有」多少根 */
    private int span;

    /** span - bars */
    private int missing;

    /** 相邻两根之间的最大间隔（秒）；只有一根时为 0 */
    private long maxGap;

    /** 时间断档，按时间递增 */
    private List<Gap> gaps;

    /** 这一天按星期几应当有多少根；日期认不出来时为空 */
    private List<Integer> expect;

    /** 根数不在 expect 里（expect 为空时不判） */
    private boolean barsOff;

    /** 同一天别的标的的根数与它不同；由扫完所有标的之后回填 */
    private boolean mismatch;

    private long bytes;

    /** 非空 = 这份文件没统计到（读不了） */
    private String err;
}
