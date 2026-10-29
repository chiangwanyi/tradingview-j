package com.tradingviewj.server.entity.store.scan;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 整个数据目录的摘要（对应 Go 的 {@code store.ScanResult}）。
 *
 * <p>「分析」是只读的：这份结果完全由磁盘现状算出来，不经过 {@code Store}。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class ScanResult {

    private String root;

    /** 一根 K 线多少秒（来自配置的 intervalSeconds） */
    private long step;

    /** 数据目录是否存在 */
    private boolean exists;

    private List<FolderStat> folders;

    private int days;

    private int bars;

    private long bytes;

    private int skipped;

    private int dupes;

    /** 根数与该星期应有根数不符的日文件数 */
    private int barsOff;

    /** 各标的根数不完全一致的天数 */
    private int mismatchDays;

    /** 异常情况（读不了之类的），由调用方原样打出来 */
    private List<String> notes;
}
