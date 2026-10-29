package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一段缺失的 K 线（对应 Go 的 {@code store.Gap}，也是网页报告里的 {@code Gap}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Gap {

    /** 缺失的第一根应当出现的时间（Unix 秒） */
    private long startT;

    /** 缺失的最后一根应当出现的时间 */
    private long endT;

    /** 缺了几根 */
    private int bars;
}
