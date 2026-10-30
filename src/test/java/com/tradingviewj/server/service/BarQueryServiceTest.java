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
 * <p>钉住四条规则：清单里有的标的才认、不传日期落到最新一天、休市日返回空数组不算失败、
 * 缺文件/缺标的要报成能看懂的业务错误。
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
    void readsRequestedDayInFileOrder() {
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-04");

        assertEquals("2024-03-04", series.day());
        assertEquals("CME_MINI_MES1!", series.folder());
        assertEquals(2, series.bars().size());
        // 时间保持 CSV 原文：画布的横坐标就是这一列，换算时区只会让两边对不上
        assertEquals("2024-03-04 09:00", series.bars().get(0).time());
        assertEquals(0, new java.math.BigDecimal("5102.50").compareTo(series.bars().get(0).high()));
    }

    @Test
    void defaultsToLatestDayWhenDayOmitted() {
        assertEquals("2024-03-05", service.day("CME_MINI:MES1!", null).day());
        assertEquals("2024-03-05", service.day("CME_MINI:MES1!", "").day());
    }

    @Test
    void missingDayFileIsEmptyNotError() {
        // 休市日 / 还没抓到的日子：日历上确实有这一天，但磁盘上没有文件
        BarSeriesResponse series = service.day("CME_MINI:MES1!", "2024-03-06");

        assertEquals("2024-03-06", series.day());
        assertTrue(series.bars().isEmpty());
    }

    @Test
    void unknownSymbolSaysWhatIsAvailable() {
        BusinessException e = assertThrows(BusinessException.class,
                () -> service.day("CME_MINI:MNQ1!", null));

        assertTrue(e.getMessage().contains("CME_MINI:MES1!"), "错误里要列出清单里的标的，别只说「不存在」");
    }

    @Test
    void badDayFormatIsBusinessError() {
        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", "2024/03/04"));
        assertThrows(BusinessException.class, () -> service.day("", null));
    }

    @Test
    void symbolWithoutAnyCsvIsBusinessError() throws IOException {
        // 「有标的、没行情」是抓取程序还没跑起来的常态，要说成人话，不能是 NPE 或空图
        Path folder = dataRoot.resolve("CME_MINI_MES1!");
        Files.deleteIfExists(folder.resolve("2024-03-04.csv"));
        Files.deleteIfExists(folder.resolve("2024-03-05.csv"));

        assertThrows(BusinessException.class, () -> service.day("CME_MINI:MES1!", null));
    }

    @Test
    void emptyCatalogReturnsEmptyList() throws IOException {
        Files.delete(dataRoot.resolve(SymbolCatalog.FILE_NAME));

        assertTrue(service.symbols().isEmpty());
    }
}
