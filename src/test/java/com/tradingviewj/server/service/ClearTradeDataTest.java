package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.record.TradeTag;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.entity.trade.TradeOrder;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRepository;
import com.tradingviewj.server.repository.TradeTagRepository;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 「清空全部交易信息」和标签模型在<strong>真库上</strong>的行为（临时 SQLite，不碰 {@code ./db/}）。
 *
 * <p>纯 mock 的 {@link HistoryQueryServiceTest} 只能验「调了哪个方法、返回哪几个数」，
 * 验不到这三件事，而它们恰恰只在真库上才成立：
 *
 * <ol>
 *   <li>{@code trade_tag} / {@code trade_record_tag} 两张表能被 Hibernate 的
 *       {@code @ManyToMany + @OrderColumn} 认可 —— 映射写错只会在实际存取时炸；</li>
 *   <li>清空之后<strong>标签本子还在</strong>（这是 2026-11-01 拆分标签表的全部意义）；</li>
 *   <li>被记录引用的标签<strong>删不掉</strong>（{@code ON DELETE RESTRICT}）。
 *       这条规则写在 DDL 上而不是某个 service 的 if 里，所以只能靠真库验。</li>
 * </ol>
 */
@SpringBootTest
class ClearTradeDataTest {

    private static final String SYMBOL = "CME_MINI:MES1!";
    private static final String FOLDER = "CME_MINI_MES1!";
    private static final String ACCOUNT = "acc-clear-test";

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            Path dir = Files.createTempDirectory("tvj-clear-").toAbsolutePath();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir.resolve("test.db"));
            registry.add("app.bar-root", () -> dir.resolve("data").toString());
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    /** 开账户时会去抽一份日文件定位起点；这里一个账户都不开，纯粹让它别去找真实 data/。 */
    @MockitoBean
    private PropFirmStartPicker picker;

    @Autowired private HistoryQueryService history;
    @Autowired private TradeRecordRepository records;
    @Autowired private TradeTagRepository tags;
    @Autowired private TradeRepository trades;
    @Autowired private OrderRepository orders;
    @Autowired private SymbolRepository symbols;
    @Autowired private AuditEventRepository events;

    private String recordId;
    private String tagId;

    @BeforeEach
    void seed() {
        // 清场顺序就是外键链：成交 → 记录（删记录时 trade_record_tag 按 CASCADE 跟着走）→
        // 挂单 → 标签（此时已无引用，RESTRICT 不拦）→ 标的（被上面全部表引用，最后删）
        trades.deleteAll();
        records.deleteAll();
        orders.deleteAll();
        tags.deleteAll();
        symbols.deleteAll();

        symbols.save(Symbol.of(SYMBOL, FOLDER, new BigDecimal("0.50")));

        TradeRecord record = records.save(TradeRecord.open(
                ACCOUNT, "清空测试账户", SYMBOL, "2024-03-04 09:00", 1, Actor.HUMAN, "我"));
        recordId = record.getId();

        TradeTag tag = tags.save(TradeTag.of("追单", "#ff3b30"));
        tagId = tag.getId();

        // 打标签：标签是独立实体，先存再挂（@ManyToMany 没有 cascade，正是要它别偷偷建标签）
        record.setTags(List.of(tag));
        records.saveAndFlush(record);

        orders.save(TradeOrder.place(ACCOUNT, SYMBOL, Side.BUY, TradeOrder.Type.LIMIT,
                new BigDecimal("4000.00"), BigDecimal.ONE, "2024-03-04 09:05"));
        trades.save(Trade.fill(ACCOUNT, SYMBOL, "order-x", "2024-03-04 09:10", 1,
                Side.BUY, new BigDecimal("4000.00"), BigDecimal.ONE,
                BigDecimal.ZERO, new BigDecimal("0.50")));
    }

    /** 4 张表清空、分表报数、**标签本子留下** —— 这是这次拆表的全部目的。 */
    @Test
    void clearsFourTablesButKeepsTheTagBook() {
        var result = history.clearTradeData();

        assertEquals(1L, result.orders(), "挂单");
        assertEquals(1L, result.trades(), "成交");
        assertEquals(1L, result.records(), "交易记录");
        assertEquals(1L, result.tagLinks(), "记录与标签的关联行");

        assertEquals(0, records.count());
        assertEquals(0, trades.count());
        assertEquals(0, orders.count());
        assertEquals(1, tags.count(), "标签字典不该被清 —— 那是用户自己攒的本子");
        assertNotNull(tags.findById(tagId).orElse(null), "同一个标签还在，不是被重建了一个同名空壳");
        assertEquals("追单", tags.findById(tagId).orElseThrow().getName());
    }

    /** 打过的标签能读回来：{@code @ManyToMany + @OrderColumn} 的映射在真库上真的成立。 */
    @Test
    void tagsSurviveARoundTrip() {
        TradeRecord reread = records.findById(recordId).orElseThrow();

        assertEquals(1, reread.getTags().size());
        assertEquals(tagId, reread.getTags().get(0).getId());
        assertEquals("#ff3b30", reread.getTags().get(0).getColor());
    }

    /**
     * 被记录引用的标签删不掉（{@code ON DELETE RESTRICT}）。
     *
     * <p>这条是用户 2026-11-01 定的规则：删标签要付一次显式代价，不能一次误点
     * 就静默抹掉几十条历史交易上的记号。摘掉引用之后才允许删 —— 后半段一起验，
     * 否则「不许删」可能只是「永远删不了」。
     *
     * <p><strong>断言只认「消息里说了外键」，不认异常类型</strong>：SQLite 的 JDBC 驱动
     * 把外键违规报成 {@code SQLITE_CONSTRAINT_TRIGGER}（它内部确实是用 trigger 的 RAISE
     * 实现外键的），Spring 因此转译成 {@code JpaSystemException} 而不是通常以为的
     * {@code DataIntegrityViolationException}。按类型断言会把「换个驱动就废」写进测试里。
     */
    @Test
    void referencedTagCannotBeDeletedButBecomesDeletableAfterUnlink() {
        RuntimeException refused = assertThrows(RuntimeException.class, () -> tags.deleteById(tagId),
                "还被记录引用着就不许删");
        assertTrue(rootMessage(refused).toUpperCase().contains("FOREIGN KEY"),
                "拒绝的理由必须是外键约束，不是别的错：" + rootMessage(refused));
        assertEquals(1, tags.count(), "删失败之后标签必须还在");

        TradeRecord record = records.findById(recordId).orElseThrow();
        record.setTags(List.of());
        records.saveAndFlush(record);

        tags.deleteById(tagId);
        assertEquals(0, tags.count(), "没人用它了，就该能删掉");
    }

    /** 最里层那句人话（Hibernate 会把真正的原因套好几层）。 */
    private static String rootMessage(Throwable e) {
        Throwable root = e;
        while (root.getCause() != null) {
            root = root.getCause();
        }
        return root.getMessage() == null ? "" : root.getMessage();
    }

    /** 清空交易信息<strong>不碰</strong>操作记录：两个入口各管各的。 */
    @Test
    void leavesAuditEventsAlone() {
        events.save(com.tradingviewj.server.entity.audit.AuditEvent.of(
                ACCOUNT, SYMBOL, "2024-03-04 09:05", 1, Actor.HUMAN,
                com.tradingviewj.server.enums.ReplayAction.ORDER_PLACED, "{}"));

        history.clearTradeData();

        assertTrue(events.count() > 0, "操作记录该原样留着");
    }
}
