package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.drawing.DrawingStyle;
import com.tradingviewj.server.entity.drawing.FibLevel;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.record.RecordTag;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.record.TradeRecordStep;
import com.tradingviewj.server.entity.session.Indicator;
import com.tradingviewj.server.entity.session.ReplaySession;
import com.tradingviewj.server.entity.strategy.Strategy;
import com.tradingviewj.server.entity.strategy.StrategyBot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.repository.AudioClipRepository;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.DrawingRepository;
import com.tradingviewj.server.repository.IndicatorRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.ReplaySessionRepository;
import com.tradingviewj.server.repository.StrategyBotRepository;
import com.tradingviewj.server.repository.StrategyRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRecordStepRepository;
import com.tradingviewj.server.repository.TradeRepository;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 持久化冒烟测试：每一类实体存一次、读一次。
 *
 * <p>它验证的是<strong>只有真跑一次才知道</strong>的那几件事：
 *
 * <ul>
 *   <li>{@code schema.sql} 和实体映射对得上（列名、@Embedded 的前缀、@ElementCollection 的从表）；</li>
 *   <li>{@code BigDecimal} 经 TEXT 往返<strong>一位不差</strong>（这是整个金额方案成立的前提）；</li>
 *   <li>{@code Instant} 经 INTEGER 毫秒往返；</li>
 *   <li>SQLite 方言真的能建出这些表、能 insert、能按方法名派生查询。</li>
 * </ul>
 *
 * <p>用临时文件库而不是 {@code :memory:}：Hikari 只有一条连接，但连接一旦被回收，
 * 内存库就跟着消失了 —— 那种失败看起来像「表不见了」，极难定位。
 */
@SpringBootTest
class PersistenceSmokeTest {

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-test-").toAbsolutePath().toString();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir + "/test.db");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private SymbolRepository symbols;
    @Autowired private ReplaySessionRepository sessions;
    @Autowired private IndicatorRepository indicators;
    @Autowired private DrawingRepository drawings;
    @Autowired private OrderRepository orders;
    @Autowired private TradeRepository trades;
    @Autowired private AudioClipRepository audioClips;
    @Autowired private AuditEventRepository auditEvents;
    @Autowired private StrategyRepository strategies;
    @Autowired private StrategyBotRepository bots;
    @Autowired private TradeRecordRepository records;
    @Autowired private TradeRecordStepRepository steps;
    @Autowired private PropFirmTemplateRepository templates;
    @Autowired private PropFirmAccountRepository accounts;

    @Test
    void everyAggregateRoundTrips() {
        // ---- 标的：主键就是标的代码，刻度用 BigDecimal ----
        Symbol symbol = symbols.save(Symbol.of("CME_MINI:MNQ1!", "CME_MINI_MNQ1!"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("0.5"));
        symbols.save(symbol);

        Symbol reloadedSymbol = symbols.findByFolder("CME_MINI_MNQ1!").orElseThrow();
        assertEquals("CME_MINI:MNQ1!", reloadedSymbol.getId());
        // 精确相等，不是 compareTo == 0：TEXT 存储不允许出现 0.2500000001
        assertEquals(new BigDecimal("0.25"), reloadedSymbol.getTick());
        assertNotNull(reloadedSymbol.getCreatedAt());

        // ---- 会话 + 指标 ----
        ReplaySession session = sessions.save(
                ReplaySession.open(symbol.getId(), "2026-10-16 12:00", "jiang"));
        assertEquals(1, sessions.findBySymbolId(symbol.getId()).size());

        indicators.save(Indicator.of(session.getId(), Indicator.Type.EMA, 20, Indicator.Source.CLOSE));
        assertEquals(1, indicators.findBySessionIdOrderBySortOrderAsc(session.getId()).size());

        // ---- 图形：两个 @Embedded 锚点靠 @AttributeOverride 分列 + 斐波那契从表 ----
        Drawing drawing = Drawing.of(session.getId(), Drawing.Kind.FIB,
                new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0),
                new Anchor("2026-10-16 15:00", new BigDecimal("21300.5"), 40),
                "jiang");
        drawing.setStyle(new DrawingStyle("#ef4444", new BigDecimal("1.5"),
                DrawingStyle.Dash.SOLID, "#ef4444", new BigDecimal("0.08")));
        drawing.setFibLevels(List.of(
                new FibLevel(new BigDecimal("0"), "#888888"),
                new FibLevel(new BigDecimal("0.618"), "#f59e0b")));
        drawings.save(drawing);

        Drawing reloadedDrawing = drawings.findBySessionId(session.getId()).get(0);
        assertEquals(new BigDecimal("21300.5"), reloadedDrawing.getEnd().getPrice());
        assertEquals(40, reloadedDrawing.getEnd().getOffset());
        assertEquals(2, reloadedDrawing.getFibLevels().size());
        assertEquals(new BigDecimal("0.618"), reloadedDrawing.getFibLevels().get(1).getRatio());

        // ---- 挂单 + 成交 ----
        Order order = Order.place(session.getId(), Side.BUY, Order.Type.LIMIT,
                new BigDecimal("21250"), new BigDecimal("1"), "2026-10-16 12:00", 0, null);
        orders.save(order);
        order.markFilled("2026-10-16 12:05", 1, new BigDecimal("21250"));
        orders.save(order);
        assertEquals(Order.Status.FILLED, orders.findById(order.getId()).orElseThrow().getStatus());

        Trade trade = trades.save(Trade.fill(session.getId(), order.getId(), "2026-10-16 12:05",
                Side.BUY, new BigDecimal("21250"), new BigDecimal("1"), BigDecimal.ZERO));
        assertEquals(1, trades.findBySessionIdOrderByBarTimeAscIdAsc(session.getId()).size());

        // ---- 语音 ----
        audioClips.save(AudioClip.record(session.getId(), "2026-10-16 12:00", 0,
                session.getId() + "/voice-0.wav", 3200, 65536, "jiang"));
        assertEquals(1, audioClips.countBySessionIdAndBarTime(session.getId(), "2026-10-16 12:00"));

        // ---- 操作记录：会话内 seq 连续 ----
        auditEvents.save(AuditEvent.of(session.getId(), 0, "human", "bar_advanced", "{\"cursor\":1}"));
        auditEvents.save(AuditEvent.of(session.getId(), 1, "human", "order_placed", "{}"));
        assertEquals(1, auditEvents.findTopBySessionIdOrderBySeqDesc(session.getId()).orElseThrow().getSeq());

        // ---- 策略 + 机器人 ----
        Strategy strategy = strategies.save(Strategy.register("caveman", "Caveman", "野人", "{}"));
        StrategyBot bot = bots.save(StrategyBot.start(session.getId(), strategy.getId(),
                new BigDecimal("1"), 300));
        bot.stop(StrategyBot.Status.FINISHED, "数据到头", 4);
        bots.save(bot);
        assertEquals(4, bots.findBySessionIdOrderByStartedAtDesc(session.getId()).get(0).getEntryCount());

        // ---- PF 模板 + 账户：规则是副本，改模板不影响已开的账户 ----
        PropFirmTemplate template = templates.save(PropFirmTemplate.define("Apex 50K", "50K 考核",
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2500.00"), new BigDecimal("1250.00"),
                        PropFirmRules.DrawdownType.TRAILING_EOD, 5, new BigDecimal("0.90"))));
        PropFirmAccount account = accounts.save(PropFirmAccount.open(template, "第一次考核"));

        assertEquals(new BigDecimal("50000.00"), accounts.findById(account.getId()).orElseThrow().getBalance());
        assertEquals(PropFirmAccount.Status.ACTIVE, account.getStatus());

        template.setName("Apex 50K（改名）");
        template.getRules().setMaxDrawdown(new BigDecimal("9999.00"));
        templates.save(template);
        assertEquals(new BigDecimal("2500.00"),
                accounts.findById(account.getId()).orElseThrow().getRules().getMaxDrawdown(),
                "模板改了，账户上的规则快照不能跟着变");

        // ---- 交易记录 + 步骤 + 标签 ----
        TradeRecord record = TradeRecord.open(session.getId(), "2026-10-16 12:00",
                TradeRecord.Source.HUMAN, "jiang");
        record.setAccountId(account.getId());
        record.openPosition(Side.BUY, "2026-10-16 12:05", new BigDecimal("21250"), new BigDecimal("1"));
        record.closeAt("2026-10-16 13:00", new BigDecimal("21260"), new BigDecimal("20"));
        record.setTags(List.of(new RecordTag("追单", "#ef4444")));
        records.save(record);

        steps.save(TradeRecordStep.of(record.getId(), 0, "base", "2026-10-16 12:00", 0,
                "human", null, "{\"events\":[]}"));
        steps.save(TradeRecordStep.of(record.getId(), 1, "order_filled", "2026-10-16 12:05", 1,
                "human", null, "{\"events\":[]}"));

        TradeRecord reloadedRecord = records.findById(record.getId()).orElseThrow();
        assertTrue(reloadedRecord.isClosed());
        assertEquals(new BigDecimal("20"), reloadedRecord.getRealizedPnl());
        assertEquals(1, reloadedRecord.getTags().size());
        assertEquals(2, steps.findByRecordIdOrderByStepNoAsc(record.getId()).size());
        assertEquals(record.getId(), records.findByAccountIdOrderByEntryBarTimeAsc(account.getId()).get(0).getId());

        // 成交挂到记录上（弱引用，删记录不影响成交）
        trade.setTradeRecordId(record.getId());
        trades.save(trade);
        assertEquals(1, trades.findByTradeRecordIdOrderByBarTimeAscIdAsc(record.getId()).size());
    }
}
