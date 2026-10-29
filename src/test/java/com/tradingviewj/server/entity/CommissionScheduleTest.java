package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.market.CommissionSchedule;
import com.tradingviewj.server.repository.CommissionScheduleRepository;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

/**
 * 手续费价目表：查价口径 + 落库形状。
 *
 * <p>钉住三件事，这三件事错了都不会抛异常，只会安静地算错钱：
 *
 * <ul>
 *   <li>键是<strong>品种根名</strong>：{@code CME_MINI:MES1!} 必须能查到 {@code MES} 的价；</li>
 *   <li>查不到是 {@code null} 而<strong>不是 0</strong>——「漏配」和「免手续费」必须能区分；</li>
 *   <li>{@code @ElementCollection} 的从表和 {@code schema.sql} 对得上，金额经 TEXT 往返一位不差。</li>
 * </ul>
 *
 * <p>用临时文件库而不是 {@code :memory:}，理由同 {@code PersistenceSmokeTest}：连着池被回收内存库就没了。
 */
@SpringBootTest
class CommissionScheduleTest {

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-commission-test-").toAbsolutePath().toString();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir + "/test.db");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private CommissionScheduleRepository schedules;
    @Autowired private JdbcTemplate jdbc;

    @Test
    void rateIsLookedUpByRootAndRoundTrips() {
        // 和 data/_symbols.json 的 commission 段完全一致
        CommissionSchedule schedule = CommissionSchedule.of(CommissionSchedule.FeeType.PER_CONTRACT_PER_SIDE)
                .withRate("MES", new BigDecimal("0.50"))
                .withRate("MNQ", new BigDecimal("0.50"))
                .withRate("MGC", new BigDecimal("0.80"));
        schedules.save(schedule);

        CommissionSchedule reloaded = schedules.findById(CommissionSchedule.SINGLETON_ID).orElseThrow();
        assertEquals(CommissionSchedule.FeeType.PER_CONTRACT_PER_SIDE, reloaded.getType());
        assertEquals(3, reloaded.getRates().size());
        // 精确相等：TEXT 存储不允许出现 0.8000000000000001
        assertEquals(new BigDecimal("0.80"), reloaded.getRates().get("MGC"));

        // 完整标的代码 → 根名 → 费率
        assertEquals(new BigDecimal("0.50"), reloaded.rateFor("CME_MINI:MES1!"));
        assertEquals(new BigDecimal("0.50"), reloaded.rateFor("CME_MINI:MNQ1!"));
        assertEquals(new BigDecimal("0.80"), reloaded.rateFor("COMEX_MINI:MGC1!"));
        // 没有配的品种（CL 在 _symbols.json 里有 tick，但 commission 里没有）
        assertNull(reloaded.rateFor("NYMEX:CL1!"));

        // 单边 = 每手每边费率 × 手数；往返由调用方决定乘不乘 2
        assertEquals(new BigDecimal("1.60"), reloaded.perSideFee("COMEX_MINI:MGC1!", 2));
        assertNull(reloaded.perSideFee("NYMEX:CL1!", 1));

        // 从表真的落了 3 行，且键存的是根名（不是完整代码）
        Integer rows = jdbc.queryForObject(
                "select count(*) from commission_rate where schedule_id = ?", Integer.class,
                CommissionSchedule.SINGLETON_ID);
        assertEquals(3, rows);
        assertNotNull(jdbc.queryForObject(
                "select rate from commission_rate where symbol_root = 'MGC'", String.class));
    }

    @Test
    void rootOfStripsExchangePrefixAndMonthSuffix() {
        assertEquals("MES", CommissionSchedule.rootOf("CME_MINI:MES1!"));
        assertEquals("CL", CommissionSchedule.rootOf("NYMEX:CL1!"));
        // 没有交易所前缀也认
        assertEquals("MNQ", CommissionSchedule.rootOf("MNQ"));
        // 数字开头的品种保留整串，而不是猜一个
        assertEquals("6E1!", CommissionSchedule.rootOf("CME:6E1!"));
    }
}
