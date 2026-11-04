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
 * 抽起点的失败路径 + 两条成功路径。
 *
 * <p>失败路径是重点：「symbol 表为空」「标的不认识」「目录里没有 csv」现象差不多、
 * 原因完全不同，报错必须点对方向。成功路径要钉住 2026-10-30 之后的新语义：
 * <strong>只在账户绑定的那个标的的目录里抽</strong>，别的标的的数据再全也不该被选中。
 */
class PropFirmStartPickerTest {

    private static final String MES = "CME_MINI:MES1!";
    private static final String MNQ = "CME_MINI:MNQ1!";

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

    private static SymbolRepository repo(Symbol... symbols) {
        SymbolRepository repository = mock(SymbolRepository.class);
        when(repository.findAll()).thenReturn(List.of(symbols));
        return repository;
    }

    @Test
    void emptySymbolTableSaysTheRealReason() {
        PropFirmStartPicker picker = new PropFirmStartPicker(repo(), bars());

        BusinessException e = assertThrows(BusinessException.class, () -> picker.pick(MES));

        assertTrue(e.getMessage().contains("symbol 表为空"), e.getMessage());
        // 不能再把方向指到 data/ 上：这次的 csv 一个都没少
        assertTrue(!e.getMessage().contains("data/ 下没有"), e.getMessage());
    }

    /** 没给标的：当场拒绝，别退化成「随便挑一个」。 */
    @Test
    void blankSymbolIsRejected() {
        PropFirmStartPicker picker = new PropFirmStartPicker(repo(symbol(MES, "CME_MINI_MES1!")), bars());

        BusinessException e = assertThrows(BusinessException.class, () -> picker.pick("  "));
        assertTrue(e.getMessage().contains("symbolId"), e.getMessage());
    }

    /** 标的不在 symbol 表里：把库里到底有哪些一并说出来。 */
    @Test
    void unknownSymbolListsWhatIsKnown() {
        PropFirmStartPicker picker = new PropFirmStartPicker(repo(symbol(MES, "CME_MINI_MES1!")), bars());

        BusinessException e = assertThrows(BusinessException.class, () -> picker.pick(MNQ));
        assertTrue(e.getMessage().contains("不认识的标的"), e.getMessage());
        assertTrue(e.getMessage().contains(MES), "报错要带上清单里现有的标的: " + e.getMessage());
    }

    @Test
    void symbolsExistButTheirFoldersHaveNoCsv() {
        PropFirmStartPicker picker = new PropFirmStartPicker(repo(symbol(MES, "CME_MINI_MES1!")), bars());

        BusinessException e = assertThrows(BusinessException.class, () -> picker.pick(MES));

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

        PropFirmStartPicker picker = new PropFirmStartPicker(repo(symbol(MES, "CME_MINI_MES1!")), bars());

        PropFirmStartPicker.Pick pick = picker.pick(MES);

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

        PropFirmStartPicker picker = new PropFirmStartPicker(repo(symbol(MES, "CME_MINI_MES1!")), bars());

        assertEquals("2024-03-04 09:30", picker.pick(MES).barTime());
    }

    /**
     * 只在自己的目录里抽：另一个标的目录里全是数据，也一根都不该被选中。
     *
     * <p>这是 2026-10-30「账户绑定单一标的」的落点 —— 起点必须在账户自己的图上存在，
     * 否则日历、可交易日、回放起点全都建在一个不存在的坐标上。
     */
    @Test
    void onlyPicksFromTheBoundSymbolsFolder() throws IOException {
        writeDay("CME_MINI_MNQ1!", "2024-03-05", """
                time,open,high,low,close,volume
                2024-03-05 09:00,100,101,99,100.5,10
                """);

        PropFirmStartPicker picker = new PropFirmStartPicker(
                repo(symbol(MES, "CME_MINI_MES1!"), symbol(MNQ, "CME_MINI_MNQ1!")), bars());

        // MES 目录下一个 csv 都没有 —— 哪怕 MNQ 有数据，也不能拿 MNQ 的起点顶上
        BusinessException e = assertThrows(BusinessException.class, () -> picker.pick(MES));
        assertTrue(e.getMessage().contains("CME_MINI_MES1!"), e.getMessage());
        assertTrue(!e.getMessage().contains("CME_MINI_MNQ1!"), e.getMessage());

        // 换成 MNQ 就能抽到它自己的那天
        PropFirmStartPicker.Pick pick = picker.pick(MNQ);
        assertEquals("CME_MINI_MNQ1!", pick.folder());
        assertEquals("2024-03-05 09:00", pick.barTime());
    }

    private void writeDay(String folder, String date, String content) throws IOException {
        Path dir = barRoot.resolve(folder);
        Files.createDirectories(dir);
        Files.writeString(dir.resolve(date + ".csv"), content);
    }
}
