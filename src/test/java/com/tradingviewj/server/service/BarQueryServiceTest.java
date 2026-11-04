package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.SymbolSummaryResponse;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolCatalog;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 交易页取数：临时目录里放一份 {@code _symbols.json} + 两天的 CSV，不连库、不起 Spring。
 *
 * <p>钉住这些规则：清单里有的标的才认；上一交易日按磁盘上的文件算（不是日历减一天）；
 * 截止时刻只截「截止那一天」；不传时刻落到最新一天；休市日返回空数组不算失败；
 * 缺文件/缺标的/格式不对要报成能看懂的业务错误。
 */
class BarQueryServiceTest {

    @TempDir
    Path dataRoot;

    private BarQueryService service;

    @BeforeEach
    void setUp() throws IOException {
        Files.writeString(dataRoot.resolve(SymbolCatalog.FILE_NAME), """
                {
                  "version": 1,
                  "symbols": {
                    "CME_MINI:MES1!": {
                      "folder": "CME_MINI_MES1!",
                      "tick": 0.25,
                      "tickValue": 1.25,
                      "commission": 0.50
                    }
                  }
                }
                """);
        Path folder = Files.createDirectories(dataRoot.resolve("CME_MINI_MES1!"));
        Files.writeString(folder.resolve("2024-03-04.csv"), """
                time,open,high,low,close,volume
                2024-03-04 09:00,5100.25,5102.50,5099.75,5101.00,1200
                2024-03-04 09:05,5101.00,5103.00,5100.50,5102.75,900
                """);
        Files.writeString(folder.resolve("2024-03-05.csv"), """
                time,open,high,low,close,volume
                2024-03-05 09:00,5110.00,5111.00,5108.50,5109.25,800
                2024-03-05 09:05,5109.25,5112.00,5109.00,5111.50,700
                """);

        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        service = new BarQueryService(new CsvBarStore(properties), new SymbolCatalog(properties));
    }

    @Test
    void listsSymbolsWithDayFileRange() {
        List<SymbolSummaryResponse> list = service.symbols();

        assertEquals(1, list.size());
        SymbolSummaryResponse mes = list.get(0);
        assertEquals("CME_MINI:MES1!", mes.code());
        assertEquals(2, mes.days());
        assertEquals("2024-03-04", mes.firstDay());
        assertEquals("2024-03-05", mes.lastDay());
        assertEquals(0, new java.math.BigDecimal("0.25").compareTo(mes.tick()));
    }

    @Test
    void returnsPreviousTradingDayPlusBarsUpToTheMoment() {
        // 上一交易日整天（2 根） + 截止那天 09:00 那一根
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-05 09:00");

        assertEquals("2024-03-05", series.day());
        assertEquals("CME_MINI_MES1!", series.folder());
        assertEquals(3, series.bars().size());
        // 时间保持 CSV 原文：画布的横坐标就是这一列，换算时区只会让两边对不上
        assertEquals("2024-03-04 09:00", series.bars().get(0).time());
        assertEquals("2024-03-04 09:05", series.bars().get(1).time());
        assertEquals("2024-03-05 09:00", series.bars().get(2).time());
        assertEquals(0, new java.math.BigDecimal("5102.50").compareTo(series.bars().get(0).high()));
    }

    @Test
    void cutoffOnlyTrimsTheTargetDayNotTheOvernightPart() {
        // 09:04 时 09:05 那根还没发生；但上一交易日的两根必须整段保留
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-05 09:04");

        assertEquals(3, series.bars().size());
        assertEquals("2024-03-05 09:00", series.bars().get(series.bars().size() - 1).time());

        // 截止到开盘前：当天的根一根都不要，只剩隔夜那一段
        BarSeriesResponse beforeOpen = service.day("CME_MINI:MES1!", "2024-03-05 00:00");
        assertEquals(2, beforeOpen.bars().size());
        assertEquals("2024-03-04 09:05", beforeOpen.bars().get(1).time());
    }

    @Test
    void earliestDayHasNoPreviousTradingDay() {
        // 磁盘上最早的那一天没有「上一交易日」，不能凭空造一段出来
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-04 09:05");

        assertEquals("2024-03-04", series.day());
        assertEquals(2, series.bars().size());
    }

    @Test
    void defaultToLatestDayWhenTimeOmitted() {
        // 不传时刻 = 最新一天整天（含它的上一交易日）
        assertEquals("2024-03-05", service.day("CME_MINI:MES1!", null).day());
        assertEquals(4, service.day("CME_MINI:MES1!", null).bars().size());
        assertEquals("2024-03-05", service.day("CME_MINI:MES1!", "").day());
    }

    @Test
    void emptyDayFileStillKeepsOvernightPart() {
        // 休市 / 还没抓到的日子：日历上确实有这一天，但磁盘上没有文件
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-06 09:00");

        assertEquals("2024-03-06", series.day());
        // 上一交易日（03-05）整天照给，只是当天一根都补不上
        assertEquals(2, series.bars().size());
        assertEquals("2024-03-05 09:05", series.bars().get(1).time());
    }

    @Test
    void acceptsCommonTimeShapes() {
        assertEquals(3, service.day("CME_MINI:MES1!", "2024-03-05T09:00").bars().size());
        assertEquals(3, service.day("CME_MINI:MES1!", "2024-03-05 09:00:00").bars().size());
        // 只给日期 = 那天 00:00
        assertEquals(2, service.day("CME_MINI:MES1!", "2024-03-05").bars().size());
    }

    @Test
    void unknownSymbolSaysWhatIsAvailable() {
        BusinessException e = assertThrows(BusinessException.class,
                () -> service.day("CME_MINI:MNQ1!", null));

        assertTrue(e.getMessage().contains("CME_MINI:MES1!"), "错误里要列出清单里的标的，别只说「不存在」");
    }

    @Test
    void badTimeFormatIsBusinessError() {
        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", "2024/03/04 09:00"));
        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", "2024-03-04 9点"));
        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", "2024-13-04 09:00"));
        assertThrows(BusinessException.class, () -> service.day("", null));
    }

    @Test
    void symbolWithoutAnyCsvIsBusinessError() throws IOException {
        // 「有标的、没行情」是抓取程序还没跑起来的常态，要说成人话，不能是 NPE 或空图
        Path folder = dataRoot.resolve("CME_MINI_MES1!");
        Files.deleteIfExists(folder.resolve("2024-03-04.csv"));
        Files.deleteIfExists(folder.resolve("2024-03-05.csv"));

        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", "2024-03-05 09:00"));
    }

    @Test
    void emptyCatalogReturnsEmptyList() throws IOException {
        Files.delete(dataRoot.resolve(SymbolCatalog.FILE_NAME));

        assertTrue(service.symbols().isEmpty());
    }
}
