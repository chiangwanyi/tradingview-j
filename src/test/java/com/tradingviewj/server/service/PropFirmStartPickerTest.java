package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.storage.CsvBarStore;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 起点抽取的口径测试：<strong>随机挑一天、定位到那天 09:00 及之后的第一根</strong>。
 *
 * <p>这里钉的是「抽不到怎么办」，而不是「随机性对不对」—— 随机数没有可断言的期望值，
 * 但「任何一次抽取都必须是一个真实存在的、可交易的 K 线」是可以断言的，
 * 而后者才是会出事的那一面。
 */
class PropFirmStartPickerTest {

    private static CsvBarStore storeOn(Path root) {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(root.toString());
        return new CsvBarStore(properties);
    }

    private static void writeDay(Path root, String folder, String day, String... barTimes)
            throws IOException {
        Path dir = Files.createDirectories(root.resolve(folder));
        StringBuilder csv = new StringBuilder("time,open,high,low,close,volume\n");
        for (String t : barTimes) {
            csv.append(t).append(",100,101,99,100,10\n");
        }
        Files.writeString(dir.resolve(day + ".csv"), csv.toString(), StandardCharsets.UTF_8);
    }

    @Test
    void locatesNineOClockOfThatDay(@TempDir Path root) throws IOException {
        writeDay(root, "CME_MINI_MES1!", "2026-10-16",
                "2026-10-16 08:55", "2026-10-16 09:00", "2026-10-16 09:05");

        PropFirmStartPicker.Pick pick = PropFirmStartPicker.pickFrom(storeOn(root),
                List.of("CME_MINI_MES1!"));

        assertEquals("2026-10-16 09:00", pick.barTime());
        assertEquals("2026-10-16.csv", pick.dayFile());
        assertEquals("CME_MINI_MES1!", pick.folder());
    }

    /**
     * 09:00 那根不存在（那天从更晚才开始）就顺延到之后的第一根，<strong>不是</strong>报错。
     *
     * <p>这条很重要：真实数据里有些日子并不是从 07:00 开始的，如果要求「必须正好有一根
     * 09:00」，开账户会随日期随机地失败 —— 那种 bug 一周才撞一次，最难查。
     */
    @Test
    void fallsForwardWhenNineOClockIsMissing(@TempDir Path root) throws IOException {
        writeDay(root, "NYMEX_CL1!", "2026-10-16", "2026-10-16 07:00", "2026-10-16 09:30");

        PropFirmStartPicker.Pick pick = PropFirmStartPicker.pickFrom(storeOn(root),
                List.of("NYMEX_CL1!"));

        assertEquals("2026-10-16 09:30", pick.barTime());
    }

    /**
     * 整天都在 09:00 之前 → 这一天不合格，换下一份文件。
     *
     * <p>随机起点可能先撞上这种日子，所以「候选不合格」必须是<strong>顺延</strong>而不是失败。
     */
    @Test
    void skipsDaysWhoseBarsAreAllBeforeNine(@TempDir Path root) throws IOException {
        writeDay(root, "A", "2026-10-01", "2026-10-01 07:00", "2026-10-01 08:00");
        writeDay(root, "A", "2026-10-02", "2026-10-02 09:00");

        // 抽 20 次都必须是那一天：不管随机落在哪份文件上，唯一合格的只有 10-02
        for (int i = 0; i < 20; i++) {
            PropFirmStartPicker.Pick pick = PropFirmStartPicker.pickFrom(storeOn(root), List.of("A"));
            assertEquals("2026-10-02 09:00", pick.barTime());
            assertEquals("2026-10-02.csv", pick.dayFile());
        }
    }

    /**
     * 一份日文件都没有 → 报错，而不是给一个假起点。
     *
     * <p>安静地返回一根不存在的 K 线，会让日历、交易日、回放全部建立在一个空值上，
     * 而且当场看不出来。宁可开不出账户。
     */
    @Test
    void refusesWhenThereIsNoDataAtAll(@TempDir Path root) {
        BusinessException e = assertThrows(BusinessException.class,
                () -> PropFirmStartPicker.pickFrom(storeOn(root), List.of("A", "B")));
        assertTrue(e.getMessage().contains("没有"), e.getMessage());
    }

    /** 全都不合格（全都停在 09:00 之前）同样是环境问题，必须当场红。 */
    @Test
    void refusesWhenNoDayIsUsable(@TempDir Path root) throws IOException {
        writeDay(root, "A", "2026-10-01", "2026-10-01 07:00");
        assertThrows(BusinessException.class,
                () -> PropFirmStartPicker.pickFrom(storeOn(root), List.of("A")));
    }
}
