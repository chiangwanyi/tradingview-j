package com.tradingviewj.server.storage;

import com.tradingviewj.server.config.AppProperties;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDate;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * K 线 CSV 读取的口径测试。
 *
 * <p>钉三件真实发生过的事：表头要跳过、坏行要跳过但不炸、目录名不能带 {@code ..}。
 */
class CsvBarStoreTest {

    private static CsvBarStore storeOn(Path root) {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(root.toString());
        return new CsvBarStore(properties);
    }

    @Test
    void parsesHeaderBadRowAndBigDecimal(@TempDir Path root) throws IOException {
        Path dir = Files.createDirectories(root.resolve("CME_MINI_MNQ1!"));
        Files.writeString(dir.resolve("2026-10-16.csv"), """
                time,open,high,low,close,volume
                2026-10-16 12:00,21250.25,21255,21248.75,21253.5,1234
                2026-10-16 12:05,21253.5,21260,21252,21258,890
                这一行是坏的
                """, StandardCharsets.UTF_8);

        List<Bar> bars = storeOn(root).readDay("CME_MINI_MNQ1!", LocalDate.of(2026, 10, 16));

        assertEquals(2, bars.size(), "表头和坏行都不算 K 线");
        assertEquals("2026-10-16 12:00", bars.get(0).barTime());
        assertEquals(new BigDecimal("21250.25"), bars.get(0).open());
        assertTrue(bars.get(1).bull());
        assertEquals(new BigDecimal("8"), bars.get(1).range());
    }

    @Test
    void missingFileIsEmptyNotError(@TempDir Path root) {
        assertTrue(storeOn(root).readDay("NOT_EXIST", LocalDate.of(2026, 10, 16)).isEmpty());
    }

    @Test
    void rejectsFolderEscapingRoot(@TempDir Path root) {
        CsvBarStore store = storeOn(root);
        assertThrows(IllegalArgumentException.class, () -> store.readDay("../../etc", LocalDate.now()));
    }
}
