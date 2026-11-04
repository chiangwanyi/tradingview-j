package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.storage.SymbolCatalog;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import javax.sql.DataSource;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.SQLException;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * {@code _symbols.json → symbol 表} 的同步，用<strong>真实的 SQLite</strong>（临时目录）钉住。
 *
 * <p>为什么不用 mock：这里要验的正好是「业务主键 = 标的代码」「commission 是
 * {@code BigDecimal → TEXT} 且 NOT NULL」这些只有真连库才走得通的部分。
 *
 * <p>钉住的行为：第一次全新建、第二次一条不写（幂等）、改了费率会就地更新、
 * 没写手续费的标的永远进不了库。最后一条是这个功能最初出问题的根因 ——
 * 表空着，开账户时连一个标的目录都找不到。
 */
@SpringBootTest
class SymbolSyncServiceTest {

    private static String testDbUrl;
    private static Path barRoot;

    @DynamicPropertySource
    static void isolatedDirs(DynamicPropertyRegistry registry) {
        try {
            Path dir = Files.createTempDirectory("tvj-symbol-").toAbsolutePath();
            barRoot = dir.resolve("data");
            Files.createDirectories(barRoot);
            testDbUrl = "jdbc:sqlite:" + dir.resolve("test.db").toString().replace('\\', '/');
            registry.add("spring.datasource.url", () -> testDbUrl);
            registry.add("app.bar-root", () -> barRoot.toString());
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private SymbolSyncService syncService;
    @Autowired private SymbolRepository symbols;
    @Autowired private DataSource dataSource;

    @BeforeEach
    void clean() throws IOException {
        symbols.deleteAll();
        Files.deleteIfExists(barRoot.resolve(SymbolCatalog.FILE_NAME));
    }

    private void writeListing(String commissionForMes) throws IOException {
        Files.writeString(barRoot.resolve(SymbolCatalog.FILE_NAME), """
                {
                  "version": 1,
                  "symbols": {
                    "CME_MINI:MES1!": {
                      "folder": "CME_MINI_MES1!",
                      "tick": 0.25,
                      "tickValue": 1.25,
                      "commission": %s
                    },
                    "NYMEX:CL1!": {
                      "folder": "NYMEX_CL1!",
                      "tick": 0.01,
                      "tickValue": 10,
                      "commission": 0
                    }
                  }
                }
                """.formatted(commissionForMes));
    }

    /** 临时库和业务库是两份文件。 */
    @Test
    void testsNeverTouchTheBusinessDatabase() throws SQLException {
        try (Connection connection = dataSource.getConnection()) {
            assertEquals(testDbUrl, connection.getMetaData().getURL());
            assertTrue(!testDbUrl.replace('\\', '/').contains("/db/tradingview-j.db"),
                    "测试库不能是业务库: " + testDbUrl);
        }
    }

    /** 第一次同步：清单里的全部标的都进库，字段一个不少。 */
    @Test
    void firstSyncCreatesEverySymbol() throws IOException {
        writeListing("0.50");

        SymbolSyncService.Report report = syncService.sync();

        assertEquals(2, report.added());
        assertEquals(0, report.updated());
        assertEquals(0, report.skipped());

        Symbol mes = symbols.findById("CME_MINI:MES1!").orElseThrow();
        assertEquals("CME_MINI_MES1!", mes.getFolder());
        assertEquals(0, new BigDecimal("0.25").compareTo(mes.getTick()));
        assertEquals(0, new BigDecimal("1.25").compareTo(mes.getTickValue()));
        assertEquals(0, new BigDecimal("0.50").compareTo(mes.getCommission()));
        assertNotNull(mes.getCreatedAt());

        // commission = 0 的「免费标的」也要进来，不能被 NOT NULL 挡在门外
        assertEquals(0, BigDecimal.ZERO.compareTo(
                symbols.findById("NYMEX:CL1!").orElseThrow().getCommission()));
    }

    /** 幂等：同一个清单跑第二次，一行都不写（否则每次启动 updated_at 全被刷新）。 */
    @Test
    void secondSyncChangesNothing() throws IOException {
        writeListing("0.50");
        syncService.sync();

        SymbolSyncService.Report report = syncService.sync();

        assertEquals(0, report.added());
        assertEquals(0, report.updated());
        assertEquals(2, report.unchanged());
    }

    /** 抓取侧把费率改了：重启服务（= 再同步一次）后，库里的值跟着变。 */
    @Test
    void changedCommissionIsWrittenBack() throws IOException {
        writeListing("0.50");
        syncService.sync();

        writeListing("0.75");
        SymbolSyncService.Report report = syncService.sync();

        assertEquals(0, report.added());
        assertEquals(1, report.updated(), "只有 MES 的费率变了，CL 不该被重写");
        assertEquals(1, report.unchanged());
        assertEquals(0, new BigDecimal("0.75").compareTo(
                symbols.findById("CME_MINI:MES1!").orElseThrow().getCommission()));
    }

    /** 没写手续费的标的进不了库 —— 「不知道多少」不是可以入库的状态。 */
    @Test
    void symbolWithoutCommissionNeverEntersTheDatabase() throws IOException {
        Files.writeString(barRoot.resolve(SymbolCatalog.FILE_NAME), """
                {
                  "symbols": {
                    "COMEX_MINI:MGC1!": {"folder": "COMEX_MINI_MGC1!", "commission": null},
                    "CME_MINI:MES1!": {"folder": "CME_MINI_MES1!", "commission": 0.5}
                  }
                }
                """);

        SymbolSyncService.Report report = syncService.sync();

        assertEquals(1, report.added());
        assertEquals(Optional.empty(), symbols.findById("COMEX_MINI:MGC1!"));
    }

    /** 清单不存在（还没抓过行情）：同步是空转，不报错、不拦启动。 */
    @Test
    void missingListingIsANoOp() {
        SymbolSyncService.Report report = syncService.sync();

        assertEquals(0, report.added() + report.updated() + report.unchanged() + report.skipped());
        assertTrue(symbols.findAll().isEmpty());
    }
}
