package com.tradingviewj.server.entity.store.catalog;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 读回来的 K 线，保留 CSV 第一列的原始文本（对应 Go 的 {@code store.TimeBar}）。
 *
 * <p>文本要留着：写回文件时按原样输出，才不会被「解析再格式化」改掉一行字。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class TimeBar {

    /** 原始文本，形如 "2026-10-16 12:00" */
    private String time;

    /** 解析后的 Unix 秒，只用于排序/比较，不进协议 */
    private long timeSeconds;

    private double open;

    private double high;

    private double low;

    private double close;

    private double volume;

    private boolean hasVolume;
}
