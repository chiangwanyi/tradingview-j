package com.tradingviewj.server.entity.report;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 全局合计（对应 Go 的 {@code webreport.Totals}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Totals {

    /** 有数据的标的目录数 */
    private int symbols;

    /** 日文件数 */
    private int days;

    /** K 线根数合计 */
    private int bars;

    /** 占用磁盘（字节） */
    private long bytes;

    /** 解析不了的行数合计 */
    private int skipped;

    /** 时间戳重复的行数合计 */
    private int dupes;

    /** 根数与应有根数不符的日文件数 */
    private int barsOff;

    /** 各标的根数不完全一致的天数（注意单位是「天」，上面两个是「份文件」） */
    private int mismatchDays;
}
