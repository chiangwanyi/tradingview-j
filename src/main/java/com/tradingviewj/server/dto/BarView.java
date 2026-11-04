package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 画布用的一根 5 分钟 K 线。
 *
 * <p>和 {@link com.tradingviewj.server.storage.Bar} 只差一个字段名：
 * 那边叫 {@code barTime}（CSV 那一列的原文），画布协议里叫 {@code time}。
 * 这个改名不是洁癖 —— 画布是从旧的复盘前端（`web/`）搬过来的，
 * 它读的就是 {@code bar.time}，让 Java 侧出同名的键，前端一行都不用改。
 *
 * <p><strong>没有 ema20</strong>：那条均线在旧实现里由 Go 复盘服务算好下发；
 * 这里只有 CSV，均线由前端现算（见 `webapp/src/data/bars.ts`）。
 *
 * @param time   K 线时间，本地时区明文，如 {@code 2024-03-04 09:00}
 */
public record BarView(
        String time,
        BigDecimal open,
        BigDecimal high,
        BigDecimal low,
        BigDecimal close,
        BigDecimal volume
) {
}
