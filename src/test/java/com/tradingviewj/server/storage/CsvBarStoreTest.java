package com.tradingviewj.server.storage;

import com.tradingviewj.server.config.AppProperties;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.time.LocalDate;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * K 线仓库的<strong>缓存与失效</strong>。
 *
 * <p>缓存是 2026-11-03 为「压测每步慢 150ms」加的，它带来一个必须钉死的风险：
 * <strong>抓取程序刚写进来的行情，服务端读不到</strong>。那种错误不会抛异常，
 * 只会让用户看到「明明录了，却推不动下一根」。所以这里逐个钉：
 *
 * <ol>
 *   <li>同一个文件读两次内容一样（缓存确实命中了，而不是每次都重读）；</li>
 *   <li>文件<strong>变长</strong>（抓取程序追加）后立刻读到新的一根；</li>
 *   <li>目录里<strong>新出现</strong>一个日文件后，{@code dayFiles} 能看到它；</li>
 *   <li>返回的集合是<strong>不可变</strong>的 —— 谁也别想改到缓存里的那份。</li>
 * </ol>
 */
class CsvBarStoreTest {

    private static final String FOLDER = "CME_MINI_MES1!";
    private static final LocalDate DAY = LocalDate.parse("2024-03-04");

    @TempDir
    Path dataRoot;

    private Path folder;
    private CsvBarStore store;

    @BeforeEach
    void setUp() throws IOException {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        store = new CsvBarStore(properties);
        folder = Files.createDirectories(dataRoot.resolve(FOLDER));
        Files.writeString(folder.resolve(DAY + ".csv"), """
                time,open,high,low,close,volume
                2024-03-04 09:00,5100.25,5102.50,5099.75,5101.00,1200
                2024-03-04 09:05,5101.00,5103.00,5100.50,5102.75,900
                """);
    }

    @Test
    void readsTheSameBarsTwice() {
        List<Bar> first = store.readDay(FOLDER, DAY);
        List<Bar> second = store.readDay(FOLDER, DAY);

        assertEquals(2, first.size());
        assertEquals("2024-03-04 09:00", first.get(0).barTime());
        assertEquals(0, first.get(0).close().compareTo(new java.math.BigDecimal("5101.00")));
        assertEquals(first, second, "第二次读到的必须和第一次一模一样（缓存命中）");
    }

    @Test
    void seesBarsAppendedAfterFirstRead() throws IOException {
        assertEquals(2, store.readDay(FOLDER, DAY).size());

        // 抓取程序往下写同一根之后的 K 线：文件变长，缓存必须失效
        Files.writeString(folder.resolve(DAY + ".csv"),
                "2024-03-04 09:10,5102.75,5104.00,5102.00,5103.50,700\n",
                StandardOpenOption.APPEND);

        List<Bar> after = store.readDay(FOLDER, DAY);
        assertEquals(3, after.size(), "追加的那一根必须立刻读得到");
        assertEquals("2024-03-04 09:10", after.get(2).barTime());
    }

    @Test
    void seesNewDayFile() throws IOException {
        assertEquals(1, store.dayFiles(FOLDER).size());

        Files.writeString(folder.resolve("2024-03-05.csv"), """
                time,open,high,low,close,volume
                2024-03-05 09:00,5110.00,5111.00,5108.50,5109.25,800
                """);

        List<Path> files = store.dayFiles(FOLDER);
        assertEquals(2, files.size(), "新落的日文件必须出现在列表里（否则【下一根】卡在昨天）");
        assertEquals("2024-03-04.csv", files.get(0).getFileName().toString(), "列表仍然按日期升序");
    }

    @Test
    void seesNewLastBarOfTheDay() throws IOException {
        assertEquals("2024-03-04 09:05", store.lastBarTime(FOLDER, DAY).orElseThrow());

        Files.writeString(folder.resolve(DAY + ".csv"),
                "2024-03-04 09:10,5102.75,5104.00,5102.00,5103.50,700\n",
                StandardOpenOption.APPEND);

        assertEquals("2024-03-04 09:10", store.lastBarTime(FOLDER, DAY).orElseThrow(),
                "「这一天走到哪一根」必须跟着文件变");
    }

    @Test
    void returnsImmutableBars() {
        List<Bar> bars = store.readDay(FOLDER, DAY);
        assertThrows(UnsupportedOperationException.class, () -> bars.add(bars.get(0)),
                "调用方不能改到缓存里那一份");
    }

    @Test
    void missingDayIsEmptyNotAnError() {
        assertTrue(store.readDay(FOLDER, LocalDate.parse("2024-03-09")).isEmpty(), "休市日没有文件");
        assertFalse(store.lastBarTime(FOLDER, LocalDate.parse("2024-03-09")).isPresent());
        assertTrue(store.dayFiles("不存在的标的").isEmpty(), "目录都没有时也要返回空表");
    }
}
