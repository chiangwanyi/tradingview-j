package com.tradingviewj.server.entity.store;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「这一天有 N 个标的各抓了 bars 根」（对应 Go 的 {@code store.BarCount}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class BarCount {

    private int bars;

    private int symbols;
}
