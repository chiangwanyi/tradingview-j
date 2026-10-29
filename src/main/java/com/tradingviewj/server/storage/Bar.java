package com.tradingviewj.server.storage;

import java.math.BigDecimal;

/**
 * 一根 5 分钟 K 线。<strong>值对象，只从 CSV 读，不进数据库</strong>。
 *
 * <p>时间用 CSV 里那一列的<strong>原始明文</strong>（{@code "2026-10-16 12:00"}），
 * 不是 {@code Instant}：它是协议里 K 线的唯一标识，也是所有图形锚点的横坐标。
 * 一旦转成时间戳，就要在「哪一列是本地时区 / 哪一列是 UTC」上反复做选择，
 * 而 CSV 里存的是本地明文 —— 转来转去必然有一天对不上。存明文，谁的时区谁清楚。
 *
 * @param barTime 本地时区明文，如 {@code 2026-10-16 12:00}
 */
public record Bar(
        String barTime,
        BigDecimal open,
        BigDecimal high,
        BigDecimal low,
        BigDecimal close,
        BigDecimal volume
) {

    /** 阳线。开盘等于收盘（十字星）不算。 */
    public boolean bull() {
        return close.compareTo(open) > 0;
    }

    /** 阴线。 */
    public boolean bear() {
        return close.compareTo(open) < 0;
    }

    /** 振幅（最高 - 最低），不会小于 0。 */
    public BigDecimal range() {
        return high.compareTo(low) <= 0 ? BigDecimal.ZERO : high.subtract(low);
    }
}
