package com.tradingviewj.server.service;

import com.tradingviewj.server.storage.CsvBarStore;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

import java.time.LocalDate;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.List;

/**
 * <strong>交易日历</strong>：某个标的有哪几天真的开过盘，以及一段区间里有几个交易日。
 *
 * <h2>为什么需要它</h2>
 *
 * <p>「已进行几个交易日」不能用日历日数、也不能按「周一到周五」数：
 * 圣诞节、元旦、MLK、独立日这些休市日既不是周六日、也没有行情，
 * 按工作日数就会多算（实测 2025-01-17 → 01-24 会数成 6 天，实际 5 天）。
 * 而交易日历<strong>不存在数据库里</strong>，它就在磁盘上 —— 一天一个 CSV，没有文件就是没开盘。
 *
 * <h2>判据：这一天有没有「属于它自己」的那一场</h2>
 *
 * <p>光看「有没有文件」不够，因为日文件是<strong>日历日</strong>切的：
 * 交易日 D = {@code [D 06:00, D+1 04:00)}，所以 {@code 2024-04-20.csv}（周六）里装的是
 * <strong>周五那场</strong> 00:00–04:55 的尾巴，它本身不是交易日。判据落在
 * 「当天最后一根 K 线是不是在 06:00 之后」（见 {@link CsvBarStore#lastBarTime}）：
 * 有自己那一场的日子，文件一定写到 23:55（半日市也写到午后），只有尾巴的日子停在 04:55 之前。
 *
 * <p>⚠️ <strong>不从文件名猜星期几</strong>：周末、节假日、数据被清理，三种情况的文件名
 * 长得一模一样，只有内容和这里是事实。也不在这里写死节假日表 —— 那是第二个真相来源。
 *
 * <h2>「归哪一天」的口径不在这里</h2>
 *
 * <p>一根 K 线属于哪个交易日由 {@link PropFirmClock#tradingDayOf} 回答，本类只回答
 * 「那个交易日到底开没开盘」。改切日时刻时只改 {@code PropFirmClock}。
 */
@Service
@RequiredArgsConstructor
public class TradingCalendar {

    private final CsvBarStore bars;

    /** 一天里「自己的那一场」开始于这一刻；最后一根早于它 = 这天只有前一天那场的尾巴。 */
    private static final String SESSION_START = String.format("%02d:%02d",
            PropFirmClock.SESSION_START.getHour(), PropFirmClock.SESSION_START.getMinute());

    /**
     * {@code [from, to]} 区间里这个标的的交易日，升序。
     *
     * @param folder 标的分目录名（{@code _symbols.json} 的 folder），不是代码
     */
    public List<LocalDate> daysBetween(String folder, LocalDate from, LocalDate to) {
        if (from == null || to == null || to.isBefore(from)) {
            return List.of();
        }
        List<LocalDate> days = new ArrayList<>();
        for (LocalDate d = from; !d.isAfter(to); d = d.plusDays(1)) {
            if (isTradingDay(folder, d)) {
                days.add(d);
            }
        }
        return days;
    }

    /** {@code [from, to]} 区间里有几个交易日（含两头）。 */
    public int countBetween(String folder, LocalDate from, LocalDate to) {
        return daysBetween(folder, from, to).size();
    }

    /**
     * 这一天开没开盘。
     *
     * <p>没有文件 → 没开（休市、或数据被清掉）；有文件但最后一根在 06:00 之前 → 那天只有
     * 前一天那场的尾巴，也不算。
     */
    public boolean isTradingDay(String folder, LocalDate date) {
        if (!bars.hasDay(folder, date)) {
            return false;
        }
        return bars.lastBarTime(folder, date)
                .map(t -> t.length() >= 16 && t.substring(11, 16).compareTo(SESSION_START) >= 0)
                .orElse(false);
    }

    /**
     * 一根 K 线「数到哪一天为止」。
     *
     * <p>正常情况下就是它所属的交易日（跨日口径见 {@link PropFirmClock#tradingDayOf}）。
     * 例外是落在那段真空 {@code [04:00, 06:00)} 里的 K 线 —— 那时候上一场刚收完、下一场还没开始，
     * 它不属于任何交易日。这时应当数到<strong>刚收完的那一场</strong>，也就是它的日历日减一天；
     * 用日历日本身会把「还没开始的那一场」算进去，凭空多一天。
     */
    public static LocalDate dayOfOrDate(String barTime) {
        LocalDateTime at = PropFirmClock.parse(barTime);
        LocalDate day = PropFirmClock.tradingDayOf(at);
        return day == null ? at.toLocalDate().minusDays(1) : day;
    }
}
