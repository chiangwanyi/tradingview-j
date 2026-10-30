package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.storage.CsvBarStore;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 【下一根】的定位规则：临时目录里放两天的 CSV，不起 Spring、不连库。
 *
 * <p>钉住的是四个会出错的边界 —— 这些错了不会报异常，只会让盘面少走一根或多跳一天：
 *
 * <ol>
 *   <li>同一天里往后一根（最常见的那条路径）；</li>
 *   <li>当天走完了要<strong>跨到下一个日文件</strong>，而且「今天还有几根」要归零；</li>
 *   <li>账户停的那天<strong>没有文件</strong>（被清理 / 那天没抓）时不能报错，要接着往后找；</li>
 *   <li>磁盘上真的没有了就该返回 null（按钮变灰），不是抛异常。</li>
 * </ol>
 */
class BarCursorServiceTest {

    private static final String FOLDER = "CME_MINI_MES1!";

    @TempDir
    Path dataRoot;

    private BarCursorService cursor;

    @BeforeEach
    void setUp() throws IOException {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        cursor = new BarCursorService(new CsvBarStore(properties));

        Path folder = Files.createDirectories(dataRoot.resolve(FOLDER));
        Files.writeString(folder.resolve("2024-03-04.csv"), """
                time,open,high,low,close,volume
                2024-03-04 09:00,5100.25,5102.50,5099.75,5101.00,1200
                2024-03-04 09:05,5101.00,5103.00,5100.50,5102.75,900
                """);
        Files.writeString(folder.resolve("2024-03-05.csv"), """
                time,open,high,low,close,volume
                2024-03-05 09:00,5110.00,5111.00,5108.50,5109.25,800
                """);
    }

    @Test
    void nextBarInSameDay() {
        BarCursorService.NextBar next = cursor.next(FOLDER, "2024-03-04 09:00");

        assertEquals("2024-03-04 09:05", next.bar().barTime());
        assertEquals(1, next.indexInDay());
        // 这一天就两根，09:05 之后当天没有了 —— 但明天还有，所以 hasNext 是 true
        assertEquals(0, next.restOfDay());
        assertTrue(next.hasNext());
    }

    @Test
    void dayRollsOverToNextFile() {
        BarCursorService.NextBar next = cursor.next(FOLDER, "2024-03-04 09:05");

        assertEquals("2024-03-05 09:00", next.bar().barTime());
        assertEquals(0, next.indexInDay());
        assertEquals(0, next.restOfDay());
        // 05 号是最后一个文件，这一根之后磁盘上真的没有了
        assertFalse(next.hasNext());
    }

    @Test
    void missingDayFileIsSkippedNotAnError() throws IOException {
        // 05 号的文件被清理掉了，账户还停在 04 号最后一根：不能报错，答案是「没有下一根」
        Files.delete(dataRoot.resolve(FOLDER).resolve("2024-03-05.csv"));
        assertNull(cursor.next(FOLDER, "2024-03-04 09:05"));

        // 账户停在 03 号深夜（那天根本没抓，没有文件）→ 要接着往后找到 04 号第一根
        BarCursorService.NextBar next = cursor.next(FOLDER, "2024-03-03 23:00");
        assertEquals("2024-03-04 09:00", next.bar().barTime());
    }

    @Test
    void stoppedBeforeFirstFileStartsAtFirstBar() {
        BarCursorService.NextBar next = cursor.next(FOLDER, "2024-03-01 09:00");
        assertEquals("2024-03-04 09:00", next.bar().barTime());
    }

    @Test
    void endOfDiskReturnsNull() {
        assertNull(cursor.next(FOLDER, "2024-03-05 09:00"));
        assertFalse(cursor.hasNext(FOLDER, "2024-03-05 09:00"));
        assertTrue(cursor.hasNext(FOLDER, "2024-03-05 08:55"));
    }

    @Test
    void unknownFolderIsEmptyNotAnException() {
        assertNull(cursor.next("NO_SUCH_FOLDER", "2024-03-04 09:00"));
    }
}
