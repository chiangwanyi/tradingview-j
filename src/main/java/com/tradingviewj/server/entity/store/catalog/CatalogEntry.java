package com.tradingviewj.server.entity.store.catalog;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「复盘选标的」用的名录项（对应 Go 的 {@code store.CatalogEntry}）。
 *
 * <p>只列目录名和文件名里的信息，不读文件内容——一个标的有 800 多个 CSV，
 * 为了列个下拉框把它们全读一遍是不可接受的。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class CatalogEntry {

    /** _symbols.json 里登记的标的；没登记就用文件夹名 */
    private String symbol;

    private String folder;

    private int days;

    /** 最早一天（文件名） */
    private String firstDate;

    /** 最晚一天（文件名） */
    private String lastDate;

    /** 最小跳动，价格轴按它定小数位 */
    private double tick;

    /** 一个 tick 值多少美元；0 = 不知道，结算退化成「每点 1 美元」 */
    private double tickValue;
}
