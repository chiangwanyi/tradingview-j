package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个标的目录的摘要（对应 Go 的 {@code store.FolderStat}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class FolderStat {

    private String folder;

    /** _symbols.json 里登记的标的；没登记就用文件夹名 */
    private String symbol;

    /** 该文件夹在索引里有登记 */
    private boolean known;

    private List<DayStat> days;

    private int bars;

    private long bytes;

    /** 坏行合计 */
    private int skipped;

    /** 重复行合计 */
    private int dupes;

    private long firstT;

    private long lastT;

    private boolean hasTime;
}
