package com.tradingviewj.server.entity.report;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 网页上的「一个标的目录」（对应 Go 的 {@code webreport.Folder}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Folder {

    /** 目录名 */
    private String folder;

    /** _symbols.json 里登记的标的；没登记就是目录名 */
    private String symbol;

    /** 是否在索引里登记过 */
    private boolean known;

    private int days;

    private int bars;

    private long bytes;

    private int skipped;

    private int dupes;

    private boolean hasTime;

    private long firstT;

    private long lastT;

    private List<Day> files;
}
