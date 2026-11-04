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
 * <h2>口径（用户 2026-11-01 确认，取代 2026-10-29 的 04:45 版本）</h2>
 *
 * <pre>
 *   交易日 D = [D 06:00, (D+1) 04:00)         ← 这中间有成交
 *   禁交易窗口 = [D 04:00, D 06:00]           ← 两端都禁，谁都不属于
 * </pre>
 *
 * <p>所以「2026-10-29 03:10 的成交」属于交易日 <b>10-28</b>；
 * 「10-29 06:00」<b>属于</b>交易日 10-29（那是新一天的起点），但它本身<b>不可交易</b> ——
 * 「归哪一天」和「能不能下单」是两个问题，见 {@link #tradingDayOf} 与 {@link #tradable}。
 *
 * <p>由此推出每个交易日的第一根可交易 K 线是 <b>06:05</b>、最后一根是 <b>03:55</b>
 * （K 线是 5 分钟一根，04:00 那根就落在窗口里了）。
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

    /**
     * 交易日结束时刻：<b>04:00</b>。
     *
     * <p>这一天在这一刻收摊 —— 日终结算发生在这里，手上的仓位也在这里被市价平掉。
     * 注意这一根 K 线<b>自己</b>已经在窗口里了，所以它只能用来平仓，不能用来开仓。
     */
    public static final LocalTime SESSION_END = LocalTime.of(4, 0);

    /** 下一交易日开始：<b>06:00</b>。这一根归新的一天，但同样不可交易。 */
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
     * 这个时刻能不能交易 —— <b>禁交易窗口 {@code [04:00, 06:00]} 两端都算关</b>。
     *
     * <p>04:00:00 不能（那是收盘那一刻，只许平仓），06:00:00 也不能（新的一天开始，
     * 但要等第一根完整的 5 分钟 K 线走完）。所以最早可交易是 <b>06:05</b>、最晚是 <b>03:55</b>。
     */
    public static boolean tradable(LocalDateTime at) {
        LocalTime time = at.toLocalTime();
        return time.isBefore(SESSION_END) || time.isAfter(SESSION_START);
    }

    public static boolean tradable(String barTime) {
        return tradable(parse(barTime));
    }

    /**
     * 这根 K 线属于哪个交易日。
     *
     * <p>⚠️ 它和 {@link #tradable} <b>不是</b>同一件事，别互相推导：
     * 06:00 那根<b>归新的一天</b>（它是这一天的第一根，跨日结算就发生在它身上），
     * 但<b>不可交易</b>。
     *
     * @return 交易日；{@code null} = 落在禁交易窗口里，不属于任何交易日
     */
    public static LocalDate tradingDayOf(LocalDateTime at) {
        LocalTime time = at.toLocalTime();
        LocalDate date = at.toLocalDate();
        if (!time.isBefore(SESSION_END) && time.isBefore(SESSION_START)) {
            // [04:00, 06:00) 属于「上一个交易日已经结束、下一个还没开始」的那段真空
            return null;
        }
        // 06:00 之前（也就是 00:00–03:59）算前一天 —— 那是"昨天那场"的尾巴
        return time.isBefore(SESSION_START) ? date.minusDays(1) : date;
    }

    public static LocalDate tradingDayOf(String barTime) {
        return tradingDayOf(parse(barTime));
    }

    /**
     * 某个交易日的截止时刻 = 次日 04:00。
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
