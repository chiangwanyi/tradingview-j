package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.storage.CsvBarStore;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDate;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 交易日历：临时目录里摆几个日文件，不起 Spring、不连库。
 *
 * <p>钉住的是三条口径 —— 错了不会报异常，只会让卡片上的「已进行 N 个交易日」多算一两天：
 *
 * <ol>
 *   <li><strong>周六那份文件不是交易日</strong>：日文件按日历日切，周六文件里只有周五那场
 *       00:00–04:55 的尾巴；</li>
 *   <li><strong>休市日不是交易日</strong>：没有文件就是没开盘，不用查节假日表；</li>
 *   <li><strong>真空 [04:00, 06:00) 里的 K 线</strong>数到<em>刚收完的那一场</em>，
 *       不能把还没开始的那一场算进去。</li>
 * </ol>
 *
 * <p>数据用 2025-01-17 ~ 01-24 这一周：2025-01-20 这一天<strong>故意不放文件</strong>，
 * 模拟「整场休市、盘上一条都没有」的那种日子 —— 这正是用户报的「多算」在真实的
 * 美股假日里出现的样子。真实数据集里更常见的形式是「有文件、但只有前一天那场的尾巴」
 * （实测：2024-03-29 / 2025-04-18 耶稣受难日、2024-12-25 / 2025-12-25 圣诞、
 * 2025-01-01 / 2026-01-01 元旦，最后一根都停在 06:00 之前）。
 */
class TradingCalendarTest {

    private static final String FOLDER = "CME_MINI_MES1!";

    @TempDir
    Path dataRoot;

    private TradingCalendar calendar;
    private Path folder;

    @BeforeEach
    void setUp() throws IOException {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        calendar = new TradingCalendar(new CsvBarStore(properties));

        folder = Files.createDirectories(dataRoot.resolve(FOLDER));
        // 交易日：17(五) 21 22 23 24(五)；20 整场休市（没有文件），18/19 是周末
        day("2025-01-17", "23:55");
        // 周六 01-18：只有周五那场的尾巴，最后一根 04:55 —— 不是交易日
        day("2025-01-18", "04:55");
        // 01-20：整场休市，没有文件
        day("2025-01-21", "23:55");
        day("2025-01-22", "23:55");
        day("2025-01-23", "23:55");
        day("2025-01-24", "23:55");
    }

    /** 造一份「最后一根在 {@code last}」的日文件，只写两行（第一根 00:00，最后一根 last）。 */
    private void day(String date, String last) throws IOException {
        String first = date + " 00:00";
        Files.writeString(folder.resolve(date + ".csv"), """
                time,open,high,low,close,volume
                %s,5100.25,5102.50,5099.75,5101.00,1200
                %s %s,5101.00,5103.00,5100.50,5102.75,900
                """.formatted(first, date, last));
    }

    @Test
    void saturdayFileIsNotATradingDay() {
        assertTrue(calendar.isTradingDay(FOLDER, LocalDate.parse("2025-01-17")), "周五开场");
        assertFalse(calendar.isTradingDay(FOLDER, LocalDate.parse("2025-01-18")), "周六只有尾巴");
    }

    @Test
    void closedDayWithoutFileIsNotATradingDay() {
        assertFalse(calendar.isTradingDay(FOLDER, LocalDate.parse("2025-01-20")), "这天没有文件 = 没开盘");
        assertTrue(calendar.isTradingDay(FOLDER, LocalDate.parse("2025-01-21")));
    }

    @Test
    void countsFiveSessionsWhenOneWeekdayIsClosed() {
        List<LocalDate> days = calendar.daysBetween(FOLDER,
                LocalDate.parse("2025-01-17"), LocalDate.parse("2025-01-24"));

        assertEquals(List.of(
                LocalDate.parse("2025-01-17"),
                LocalDate.parse("2025-01-21"),
                LocalDate.parse("2025-01-22"),
                LocalDate.parse("2025-01-23"),
                LocalDate.parse("2025-01-24")), days);
        // 按工作日数会是 6 天（17/20/21/22/23/24）—— 这就是那个 bug：休市的那天被算了进去
        assertEquals(5, calendar.countBetween(FOLDER,
                LocalDate.parse("2025-01-17"), LocalDate.parse("2025-01-24")));
    }

    @Test
    void startDayCountsAsFirstDay() {
        assertEquals(1, calendar.countBetween(FOLDER,
                LocalDate.parse("2025-01-21"), LocalDate.parse("2025-01-21")));
    }

    @Test
    void deadZoneBarCountsThroughTheSessionThatJustEnded() {
        // 2025-01-22 04:30 落在 [04:00, 06:00) 真空里：属于谁的都不是，应数到刚收完的 01-21
        assertEquals(LocalDate.parse("2025-01-21"), TradingCalendar.dayOfOrDate("2025-01-22 04:30"));
        // 数到 01-21 为止：17 和 21 两天，22 那一场虽然盘上已有文件但还没开始，不算
        assertEquals(2, calendar.countBetween(FOLDER,
                LocalDate.parse("2025-01-17"), TradingCalendar.dayOfOrDate("2025-01-22 04:30")));
        // 06:00 之后就是新的一天了
        assertEquals(LocalDate.parse("2025-01-22"), TradingCalendar.dayOfOrDate("2025-01-22 06:00"));
    }

    @Test
    void emptyFolderCountsNothing() {
        assertEquals(0, calendar.countBetween("NO_SUCH_FOLDER",
                LocalDate.parse("2025-01-21"), LocalDate.parse("2025-01-24")));
    }
}
