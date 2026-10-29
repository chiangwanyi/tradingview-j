package com.tradingviewj.server.service;

import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;

/**
 * 交易日的唯一口径。整个 PF 考核里「今天」「日终」「今天赚了多少」全部从这里来。
 *
 * <h2>口径（用户 2026-10-29 确认）</h2>
 *
 * <pre>
 *   交易日 D = [D 06:00, (D+1) 04:45)         ← 这中间有成交
 *   禁交易窗口 = [D 04:45, D 06:00)           ← 75 分钟，不允许交易，不属于任何交易日
 * </pre>
 *
 * <p>所以「2026-10-29 03:10 的成交」属于交易日 <b>10-28</b>；
 * 「10-29 06:00」属于 <b>10-29</b>；「10-29 05:00」<b>不属于任何一天</b>，它落在禁交易窗口里。
 *
 * <p><strong>不做夏/冬令时换算</strong>：用户明确要求简化为这两个固定时刻。
 * 换来的是「这根 K 线归哪天」变成一个字符串比较，不会在某天早上整体偏移一小时。
 *
 * <h2>为什么时间直接读 CSV 明文</h2>
 *
 * <p>CSV 的 {@code bar_time} 列是本地明文 {@code "2026-10-29 03:10"}（见 {@code storage.Bar}），
 * 用户确认「全部认定为北京时间」。所以这里解析的是那串明文，不经过 {@code Instant} ——
 * 一旦经过时间戳，就得反复回答「这段是 UTC 还是本地」，而 CSV 里根本没有时区信息。
 *
 * <p>{@link #ZONE} 只在需要把 <b>Instant</b>（阶段开始时间、判失败时刻）换算成"哪个交易日"
 * 时才用得上，因为那几个字段在库里是 Unix 毫秒。
 */
public final class PropFirmClock {

    /** CSV 里那一列明文的时间格式。 */
    private static final DateTimeFormatter MINUTE =
            DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm");

    /** 当日截止（不含）：04:45 之后的成交算下一天 —— 但 04:45–06:00 谁都不算。 */
    public static final LocalTime SESSION_END = LocalTime.of(4, 45);

    /** 下一交易日开始：06:00。 */
    public static final LocalTime SESSION_START = LocalTime.of(6, 0);

    /** 全项目唯一的时区假设。CSV 明文就是这个时区的时间。 */
    public static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");

    private PropFirmClock() {
    }

    /**
     * 把 CSV 明文解析成时间。
     *
     * @throws IllegalArgumentException 格式不对 —— 静默跳过会让「今天怎么少了一半 K 线」查不出来
     */
    public static LocalDateTime parse(String barTime) {
        if (barTime == null || barTime.isBlank()) {
            throw new IllegalArgumentException("K 线时间不能为空");
        }
        try {
            return LocalDateTime.parse(barTime.trim(), MINUTE);
        } catch (DateTimeParseException e) {
            throw new IllegalArgumentException("K 线时间格式必须是 yyyy-MM-dd HH:mm，收到: " + barTime, e);
        }
    }

    /**
     * 这个时刻能不能交易（是否落在 04:45–05:59 的禁交易窗口之外）。
     *
     * <p>窗口边界按「含头不含尾」处理：04:45:00 属于窗口（不能交易），06:00:00 已经能交易。
     */
    public static boolean tradable(LocalDateTime at) {
        LocalTime time = at.toLocalTime();
        return !(time.compareTo(SESSION_END) >= 0 && time.compareTo(SESSION_START) < 0);
    }

    public static boolean tradable(String barTime) {
        return tradable(parse(barTime));
    }

    /**
     * 这根 K 线属于哪个交易日。
     *
     * @return 交易日；{@code null} = 落在禁交易窗口里，不属于任何交易日
     */
    public static LocalDate tradingDayOf(LocalDateTime at) {
        if (!tradable(at)) {
            return null;
        }
        LocalDate date = at.toLocalDate();
        // 06:00 之前（也就是 00:00–04:44）算前一天 —— 那是"昨天那场"的尾巴
        return at.toLocalTime().isBefore(SESSION_START) ? date.minusDays(1) : date;
    }

    public static LocalDate tradingDayOf(String barTime) {
        return tradingDayOf(parse(barTime));
    }

    /**
     * 某个交易日的截止时刻 = 次日 04:45。
     *
     * <p>阶段升级、日终结算都在这一刻发生：它既是「这一天的账结清」的时刻，
     * 也是「下一阶段开始」的时刻。
     */
    public static LocalDateTime sessionEndOf(LocalDate tradingDay) {
        return tradingDay.plusDays(1).atTime(SESSION_END);
    }

    /**
     * Instant（库里存的时间戳）→ 交易日。
     *
     * <p>用于「这笔成交 / 这次判失败发生在哪个交易日」。落在禁交易窗口里返回 {@code null}，
     * 调用方要自己决定怎么处理 —— 交易时段内不该出现这种情况。
     */
    public static LocalDate tradingDayOfInstant(java.time.Instant instant) {
        return tradingDayOf(LocalDateTime.ofInstant(instant, ZONE));
    }

    /** 交易日 → 可直接和 CSV 明文比较的字符串前缀 {@code yyyy-MM-dd}。 */
    public static String dayLabel(LocalDate tradingDay) {
        return tradingDay.toString();
    }
}
