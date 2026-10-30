package com.tradingviewj.server.storage;

import com.tradingviewj.server.config.AppProperties;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 读 {@code _symbols.json} 的规则，用临时目录里的真文件钉住 —— 不连库、不起 Spring。
 *
 * <p>要钉的就四件事：字段有没有读对、缺手续费的标的会不会被丢掉、
 * 文件不存在算不算失败、坏 JSON 会不会当场红。
 */
class SymbolCatalogTest {

    @TempDir
    Path dataRoot;

    private SymbolCatalog catalog() {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        return new SymbolCatalog(properties);
    }

    private void writeListing(String json) throws IOException {
        Files.writeString(dataRoot.resolve(SymbolCatalog.FILE_NAME), json);
    }

    @Test
    void readsCodeFolderTickValueAndCommission() throws IOException {
        writeListing("""
                {
                  "version": 1,
                  "symbols": {
                    "CME_MINI:MES1!": {
                      "folder": "CME_MINI_MES1!",
                      "tick": 0.25,
                      "tickValue": 1.25,
                      "commission": 0.50
                    },
                    "NYMEX:CL1!": {
                      "folder": "NYMEX_CL1!",
                      "tick": 0.01,
                      "tickValue": 10,
                      "commission": 0
                    }
                  }
                }
                """);

        List<SymbolSpec> specs = catalog().read();

        assertEquals(2, specs.size());
        SymbolSpec mes = specs.get(0);
        assertEquals("CME_MINI:MES1!", mes.code());
        assertEquals("CME_MINI_MES1!", mes.folder());
        assertEquals(0, new BigDecimal("0.25").compareTo(mes.tick()));
        assertEquals(0, new BigDecimal("1.25").compareTo(mes.tickValue()));
        assertEquals(0, new BigDecimal("0.5").compareTo(mes.commission()));

        // commission = 0 是合法的「免费」，不能被当成"没写"
        assertEquals(0, BigDecimal.ZERO.compareTo(specs.get(1).commission()));
    }

    @Test
    void missingFileIsNotAnError() {
        // 新环境还没抓过行情：清单不存在是正常状态，由开账户时报「标的表为空」
        assertTrue(catalog().read().isEmpty());
    }

    @Test
    void symbolWithoutCommissionIsDropped() throws IOException {
        writeListing("""
                {
                  "symbols": {
                    "COMEX_MINI:MGC1!": {"folder": "COMEX_MINI_MGC1!", "tick": 0.1, "tickValue": 1},
                    "NYMEX:CL1!": {"folder": "NYMEX_CL1!", "commission": 0}
                  }
                }
                """);

        List<SymbolSpec> specs = catalog().read();

        // symbol.commission 是 NOT NULL，缺了就不进库（免费必须显式写 0）
        assertEquals(1, specs.size());
        assertEquals("NYMEX:CL1!", specs.get(0).code());
    }

    @Test
    void symbolWithoutFolderIsDropped() throws IOException {
        writeListing("""
                {"symbols": {"CME_MINI:MES1!": {"commission": 0.5}}}
                """);

        assertTrue(catalog().read().isEmpty());
    }

    @Test
    void tickAndTickValueMayBeMissing() throws IOException {
        writeListing("""
                {"symbols": {"CME_MINI:MES1!": {"folder": "CME_MINI_MES1!", "commission": 0.5}}}
                """);

        SymbolSpec spec = catalog().read().get(0);

        // 只有刻度是「可空」的：调用方按「每点 1 美元」退化
        assertNull(spec.tick());
        assertNull(spec.tickValue());
        assertEquals(0, new BigDecimal("0.5").compareTo(spec.commission()));
    }

    @Test
    void brokenJsonFailsLoudly() throws IOException {
        // 文件在、但内容坏了 = 坏数据。静默当成空清单会变回「开了户但不知道为什么不行」
        writeListing("{ this is not json");

        assertThrows(UncheckedIOException.class, () -> catalog().read());
    }
}
