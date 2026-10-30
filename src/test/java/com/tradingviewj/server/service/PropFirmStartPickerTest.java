package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.storage.CsvBarStore;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

/**
 * 抽起点的两条失败路径 + 一条成功路径。
 *
 * <p>失败路径是重点：「symbol 表为空」和「目录里没有 csv」现象一样、原因完全不同。
 * 2026-10-30 之前两者共用一句「data/ 下没有 .csv」，导致明明有几万个 csv 却在查空目录。
 */
class PropFirmStartPickerTest {

    @TempDir
    Path barRoot;

    private CsvBarStore bars() {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(barRoot.toString());
        return new CsvBarStore(properties);
    }

    private static Symbol symbol(String code, String folder) {
        return Symbol.of(code, folder, new BigDecimal("0.5"));
    }

    @Test
    void emptySymbolTableSaysTheRealReason() {
        SymbolRepository symbols = mock(SymbolRepository.class);
        when(symbols.findAll()).thenReturn(List.of());
        PropFirmStartPicker picker = new PropFirmStartPicker(symbols, bars());

        BusinessException e = assertThrows(BusinessException.class, picker::pick);

        assertTrue(e.getMessage().contains("symbol 表为空"), e.getMessage());
        // 不能再把方向指到 data/ 上：这次的 csv 一个都没少
        assertTrue(!e.getMessage().contains("data/ 下没有"), e.getMessage());
    }

    @Test
    void symbolsExistButTheirFoldersHaveNoCsv() {
        SymbolRepository symbols = mock(SymbolRepository.class);
        when(symbols.findAll()).thenReturn(List.of(symbol("CME_MINI:MES1!", "CME_MINI_MES1!")));
        PropFirmStartPicker picker = new PropFirmStartPicker(symbols, bars());

        BusinessException e = assertThrows(BusinessException.class, picker::pick);

        assertTrue(e.getMessage().contains("没有任何 .csv"), e.getMessage());
        // 报错要点名查过哪些目录，否则下次还是不知道往哪看
        assertTrue(e.getMessage().contains("CME_MINI_MES1!"), e.getMessage());
    }

    @Test
    void picksFirstBarAtOrAfterNine() throws IOException {
        writeDay("CME_MINI_MES1!", "2024-03-04", """
                time,open,high,low,close,volume
                2024-03-04 07:00,100,101,99,100.5,10
                2024-03-04 07:05,100,101,99,100.5,10
                2024-03-04 09:00,100,101,99,100.5,10
                2024-03-04 09:05,100,101,99,100.5,10
                """);

        SymbolRepository symbols = mock(SymbolRepository.class);
        when(symbols.findAll()).thenReturn(List.of(symbol("CME_MINI:MES1!", "CME_MINI_MES1!")));
        PropFirmStartPicker picker = new PropFirmStartPicker(symbols, bars());

        PropFirmStartPicker.Pick pick = picker.pick();

        // 07:00 那两根在 09:00 之前，跳过；正好 09:00 的留下
        assertEquals("2024-03-04 09:00", pick.barTime());
        assertEquals("2024-03-04.csv", pick.dayFile());
        assertEquals("CME_MINI_MES1!", pick.folder());
    }

    /** 某天 09:00 恰好休市：顺延到之后的第一根，而不是「这天不可用」。 */
    @Test
    void fallsBackToFirstBarAfterNineWhenNineIsMissing() throws IOException {
        writeDay("CME_MINI_MES1!", "2024-03-04", """
                time,open,high,low,close,volume
                2024-03-04 09:30,100,101,99,100.5,10
                2024-03-04 09:35,100,101,99,100.5,10
                """);

        SymbolRepository symbols = mock(SymbolRepository.class);
        when(symbols.findAll()).thenReturn(List.of(symbol("CME_MINI:MES1!", "CME_MINI_MES1!")));
        PropFirmStartPicker picker = new PropFirmStartPicker(symbols, bars());

        assertEquals("2024-03-04 09:30", picker.pick().barTime());
    }

    private void writeDay(String folder, String date, String content) throws IOException {
        Path dir = barRoot.resolve(folder);
        Files.createDirectories(dir);
        Files.writeString(dir.resolve(date + ".csv"), content);
    }
}
