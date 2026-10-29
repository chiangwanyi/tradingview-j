package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 送给前端的一根 K 线（对应 Go 的 {@code replay.Bar}）。
 *
 * <p>价格在加载时已经四舍五入到两位小数：CSV 里有 89.97000000000001 这种浮点噪声，
 * 直接下发会让价格轴和提示里冒出一串尾巴。
 *
 * <p>ema20 在后端算好，前端只负责画——指标口径只有一份。ATR 不在这里：
 * 它的周期和平滑方式由用户在界面上现调，前端拿 OHLC 自己算。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Bar {

    private String time;

    private double open;

    private double high;

    private double low;

    private double close;

    private double volume;

    /** 前 19 根还没有值，序列化成 null（前端据此断开折线） */
    private Double ema20;
}
