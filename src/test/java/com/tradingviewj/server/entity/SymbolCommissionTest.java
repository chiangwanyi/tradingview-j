package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.repository.SymbolRepository;
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
 * 手续费是<strong>标的自己的参数</strong>（{@code symbol.commission}），不再是一张全局价目表。
 *
 * <p>钉住三件「错了也不会抛异常、只会安静地算错钱」的事：
 *
 * <ul>
 *   <li>费率跟着标的一起落库，经 TEXT 往返<strong>一位不差</strong>（0.80 不能变成 0.8000000000000001）；</li>
 *   <li>口径是<strong>单边收费</strong>：往返 = 单边 × 2，一笔完整的交易要收两次；</li>
 *   <li>{@code null}（没配）和 {@code 0}（明确免手续费，CL 就是这样）必须分得开 ——
 *       混成一种的话，漏配的标的会按免费结算。</li>
 * </ul>
 *
 * <p>用临时文件库而不是 {@code :memory:}：连着池被回收内存库就没了，理由同 {@code PersistenceSmokeTest}。
 */
@SpringBootTest
class SymbolCommissionTest {

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-symbol-commission-").toAbsolutePath().toString();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir + "/test.db");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private SymbolRepository symbols;
    @Autowired private JdbcTemplate jdbc;

    @Test
    void commissionRoundTripsAndIsChargedPerSide() {
        // 和 data/_symbols.json 里的三个值一致
        Symbol mes = Symbol.of("CME_MINI:MES1!", "CME_MINI_MES1!");
        mes.setTick(new BigDecimal("0.25"));
        mes.setTickValue(new BigDecimal("1.25"));
        mes.setCommission(new BigDecimal("0.50"));

        Symbol mgc = Symbol.of("COMEX_MINI:MGC1!", "COMEX_MINI_MGC1!");
        mgc.setCommission(new BigDecimal("0.80"));

        // CL 显式免手续费：0，不是「没配」
        Symbol cl = Symbol.of("NYMEX:CL1!", "NYMEX_CL1!");
        cl.setCommission(BigDecimal.ZERO);

        // 漏配：commission 压根没赋值
        Symbol unset = Symbol.of("CME_MINI:MNQ9!", "CME_MINI_MNQ9!");

        symbols.save(mes);
        symbols.save(mgc);
        symbols.save(cl);
        symbols.save(unset);

        Symbol reloaded = symbols.findById("CME_MINI:MES1!").orElseThrow();
        // 精确相等：TEXT 存储不允许出现 0.5000000000000001
        assertEquals(new BigDecimal("0.50"), reloaded.getCommission());

        // 单边：每手每边 × 手数
        assertEquals(new BigDecimal("1.50"), reloaded.perSideCommission(3));
        // 往返：开仓收一次、平仓再收一次
        assertEquals(new BigDecimal("3.00"), reloaded.roundTripCommission(3));

        assertEquals(new BigDecimal("0.80"), symbols.findById("COMEX_MINI:MGC1!").orElseThrow().getCommission());

        // 0 是「免费」，不是「没配」：返回 0.00 而不是 null
        Symbol reloadedCl = symbols.findById("NYMEX:CL1!").orElseThrow();
        assertNotNull(reloadedCl.getCommission());
        assertEquals(0, reloadedCl.roundTripCommission(5).signum());

        // 没配：调用方必须能看出来，而不是拿到一个 0
        Symbol reloadedUnset = symbols.findById("CME_MINI:MNQ9!").orElseThrow();
        assertNull(reloadedUnset.getCommission());
        assertNull(reloadedUnset.perSideCommission(1));
        assertNull(reloadedUnset.roundTripCommission(1));

        // 列真的是 TEXT：存进去的是 "0.50" 这段明文，不是被 NUMERIC 亲和性转过的实数
        assertEquals("0.50", jdbc.queryForObject(
                "select commission from symbol where id = ?", String.class, "CME_MINI:MES1!"));
    }
}
