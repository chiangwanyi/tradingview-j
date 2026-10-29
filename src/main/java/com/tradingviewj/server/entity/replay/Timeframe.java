package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个可选周期（对应 Go 的 {@code replay.Timeframe}）：id 既是协议取值，也是界面上的按钮文字。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Timeframe {

    private String id;

    /** 一根 K 线覆盖多少分钟；5m 是数据的原始周期 */
    private int minutes;
}
