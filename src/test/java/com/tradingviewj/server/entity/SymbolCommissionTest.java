package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.repository.SymbolRepository;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

/**
 * 手续费是<strong>标的自己的参数</strong>（{@code symbol.commission}），而且<strong>不能为空</strong>。
 *
 * <p>钉住四件「错了也不会抛异常、只会安静地算错钱」的事：
 *
 * <ul>
 *   <li>费率跟着标的一起落库，经 TEXT 往返<strong>一位不差</strong>（0.80 不能变成 0.8000000000000001）；</li>
 *   <li>口径是<strong>单边收费</strong>：往返 = 单边 × 2，一笔完整的交易要收两次；</li>
 *   <li>{@code 0}（明确免手续费，CL 就是这样）必须存得进去、留得住 —— 它<strong>不再</strong>是
 *       「和 NULL 长得像的两种情况之一」，因为 NULL 这条路已经被封死了；</li>
 *   <li>封死是<strong>两层</strong>的：实体（{@link Symbol#of} / {@link Symbol#setCommission}）收不了 null，
 *       数据库列也是 NOT NULL。只封一层的话，绕过实体的一次 insert 就能把 NULL 塞回库里。</li>
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
        Symbol mes = Symbol.of("CME_MINI:MES1!", "CME_MINI_MES1!", new BigDecimal("0.50"));
        mes.setTick(new BigDecimal("0.25"));
        mes.setTickValue(new BigDecimal("1.25"));

        Symbol mgc = Symbol.of("COMEX_MINI:MGC1!", "COMEX_MINI_MGC1!", new BigDecimal("0.80"));

        // CL 显式免手续费：0，不是「没配」
        Symbol cl = Symbol.of("NYMEX:CL1!", "NYMEX_CL1!", BigDecimal.ZERO);

        symbols.save(mes);
        symbols.save(mgc);
        symbols.save(cl);

        Symbol reloaded = symbols.findById("CME_MINI:MES1!").orElseThrow();
        // 精确相等：TEXT 存储不允许出现 0.5000000000000001
        assertEquals(new BigDecimal("0.50"), reloaded.getCommission());

        // 单边：每手每边 × 手数
        assertEquals(new BigDecimal("1.50"), reloaded.perSideCommission(3));
        // 往返：开仓收一次、平仓再收一次
        assertEquals(new BigDecimal("3.00"), reloaded.roundTripCommission(3));

        assertEquals(new BigDecimal("0.80"), symbols.findById("COMEX_MINI:MGC1!").orElseThrow().getCommission());

        // 0 是「免费」，不是「没配」：乘出来就是 0，而不是 null
        Symbol reloadedCl = symbols.findById("NYMEX:CL1!").orElseThrow();
        assertEquals(0, reloadedCl.getCommission().signum());
        assertEquals(0, reloadedCl.roundTripCommission(5).signum());

        // 列真的是 TEXT：存进去的是 "0.50" 这段明文，不是被 NUMERIC 亲和性转过的实数
        assertEquals("0.50", jdbc.queryForObject(
                "select commission from symbol where id = ?", String.class, "CME_MINI:MES1!"));
    }

    /** 实体层：建不出来，也改不回去。 */
    @Test
    void entityRefusesNullCommission() {
        assertThrows(IllegalArgumentException.class,
                () -> Symbol.of("CME_MINI:MNQ9!", "CME_MINI_MNQ9!", null));

        Symbol symbol = Symbol.of("CME_MINI:MNQ9!", "CME_MINI_MNQ9!", BigDecimal.ZERO);
        assertThrows(IllegalArgumentException.class, () -> symbol.setCommission(null));
        // 拒了之后原值不能被改坏
        assertEquals(0, symbol.getCommission().signum());
    }

    /**
     * 数据库层：绕过实体直接插一行不带 commission 的，必须被列约束挡下来。
     * 这一条是「实体守住了就行」的反例 —— 忘了写 @Column(nullable = false) / schema 里漏了 NOT NULL 时，
     * 这里会失败。
     */
    @Test
    void columnRefusesNullCommission() {
        assertThrows(DataAccessException.class, () -> jdbc.update(
                "insert into symbol (id, folder, created_at, updated_at) values (?, ?, ?, ?)",
                "CME_MINI:NOPE1!", "CME_MINI_NOPE1!", 0L, 0L));
    }
}
