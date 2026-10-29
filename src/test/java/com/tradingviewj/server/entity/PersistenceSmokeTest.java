package com.tradingviewj.server.entity;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.drawing.DrawingStyle;
import com.tradingviewj.server.entity.drawing.FibLevel;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.record.RecordTag;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.record.TradeRecordStep;
import com.tradingviewj.server.entity.strategy.Strategy;
import com.tradingviewj.server.entity.strategy.StrategyBot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.repository.AudioClipRepository;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.DrawingRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmStageLogRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.StrategyBotRepository;
import com.tradingviewj.server.repository.StrategyRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRecordStepRepository;
import com.tradingviewj.server.repository.TradeRepository;
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
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
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
 *
 * <p>所有子数据都挂在<strong>一个 PF 账户</strong>下：账户是唯一的操作归属点，
 * 「删账户不删数据」那条规则由 {@code PropFirmAccountLifecycleTest} 专门钉住。
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
    @Autowired private PropFirmStageLogRepository stageLogs;
    @Autowired private JdbcTemplate jdbc;

    @Test
    void everyAggregateRoundTrips() {
        // ---- 标的：主键就是标的代码，刻度用 BigDecimal ----
        Symbol symbol = symbols.save(Symbol.of("CME_MINI:MNQ1!", "CME_MINI_MNQ1!",
                new BigDecimal("0.50")));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("0.5"));
        symbols.save(symbol);

        Symbol reloadedSymbol = symbols.findByFolder("CME_MINI_MNQ1!").orElseThrow();
        assertEquals("CME_MINI:MNQ1!", reloadedSymbol.getId());
        // 精确相等，不是 compareTo == 0：TEXT 存储不允许出现 0.2500000001
        assertEquals(new BigDecimal("0.25"), reloadedSymbol.getTick());
        assertNotNull(reloadedSymbol.getCreatedAt());

        // ---- PF 模板 + 账户：所有子数据的归属点就在这里 ----
        // 规则是「一个阶段」的规则：EVAL 有目标 + 50% 一致性 + 40 手一档，没有盈利日要求
        PropFirmTemplate template = templates.save(PropFirmTemplate.define(
                "LucidFlex 50K EVAL", "50K 考试", PropFirmStage.EVAL, null,
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, 40)))));
        PropFirmAccount account = accounts.save(PropFirmAccount.open(template, "第一次考核"));

        PropFirmAccount reloadedAccount = accounts.findById(account.getId()).orElseThrow();
        assertEquals(new BigDecimal("50000.00"), reloadedAccount.getBalance());
        assertEquals(new BigDecimal("50000.00"), reloadedAccount.getBaseBalance(),
                "阶段起点余额必须单独存：升级时它会被重置，余额就不能再由成交累加反推");
        assertEquals(PropFirmAccount.Status.ACTIVE, reloadedAccount.getStatus());
        assertEquals(PropFirmStage.EVAL, reloadedAccount.getStage());
        assertEquals(new BigDecimal("52100.00"), reloadedAccount.getRules().getTrailingLockLevel());
        // 手数分档是 JSON 列，往返之后必须还是原来那两样东西
        assertEquals(1, reloadedAccount.getRules().getPositionTiers().size());
        assertEquals(40, reloadedAccount.getRules().getPositionTiers().get(0).maxContracts());
        assertEquals(0, BigDecimal.ZERO.compareTo(
                reloadedAccount.getRules().getPositionTiers().get(0).fromProfit()));

        // ---- 阶段流水：三阶段挤在同一行账户上，历史只能靠这张表 ----
        stageLogs.save(PropFirmStageLog.begin(account.getId(), 0, account.getStage(),
                account.getBaseBalance(), account.getStageStartedAt(), PropFirmStageLog.Reason.CREATED));
        List<PropFirmStageLog> history = stageLogs.findByAccountIdOrderBySeqAsc(account.getId());
        assertEquals(1, history.size());
        assertEquals(PropFirmStageLog.Reason.CREATED, history.get(0).getReason());
        assertEquals(PropFirmStage.EVAL, history.get(0).getStage());
        assertNull(history.get(0).getEndedAt(), "刚建的阶段还没结束");

        // 第二段（升上来的）+ 收尾：结束原因和进入原因是两列，不能混成一个
        PropFirmStageLog funded = stageLogs.save(PropFirmStageLog.begin(account.getId(), 1,
                PropFirmStage.FUNDED, new BigDecimal("50000.00"),
                java.time.Instant.now(), PropFirmStageLog.Reason.PROMOTED));
        funded.close(PropFirmStageLog.EndReason.FAILED, new BigDecimal("47900.00"),
                new BigDecimal("1800.00"), 3, java.time.Instant.now());
        stageLogs.save(funded);
        PropFirmStageLog reloadedLog = stageLogs.findTopByAccountIdOrderBySeqDesc(account.getId()).orElseThrow();
        assertEquals(PropFirmStageLog.Reason.PROMOTED, reloadedLog.getReason());
        assertEquals(PropFirmStageLog.EndReason.FAILED, reloadedLog.getEndReason());
        assertEquals(new BigDecimal("-2100.00"), reloadedLog.getNetPnl(), "净额 = 结束余额 − 起点余额");
        assertEquals(3, reloadedLog.getProfitDays());

        // ---- 图形：两个 @Embedded 锚点靠 @AttributeOverride 分列 + 斐波那契从表 ----
        Drawing drawing = Drawing.of(account.getId(), symbol.getId(), Drawing.Kind.FIB,
                new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0),
                new Anchor("2026-10-16 15:00", new BigDecimal("21300.5"), 40),
                "jiang");
        drawing.setStyle(new DrawingStyle("#ef4444", new BigDecimal("1.5"),
                DrawingStyle.Dash.SOLID, "#ef4444", new BigDecimal("0.08")));
        drawing.setFibLevels(List.of(
                new FibLevel(new BigDecimal("0"), "#888888"),
                new FibLevel(new BigDecimal("0.618"), "#f59e0b")));
        drawings.save(drawing);

        Drawing reloadedDrawing = drawings.findByAccountId(account.getId()).get(0);
        assertEquals(new BigDecimal("21300.5"), reloadedDrawing.getEnd().getPrice());
        assertEquals(40, reloadedDrawing.getEnd().getOffset());
        assertEquals(2, reloadedDrawing.getFibLevels().size());
        assertEquals(new BigDecimal("0.618"), reloadedDrawing.getFibLevels().get(1).getRatio());

        // ---- 挂单 + 成交 ----
        Order order = Order.place(account.getId(), symbol.getId(), Side.BUY, Order.Type.LIMIT,
                new BigDecimal("21250"), new BigDecimal("1"), "2026-10-16 12:00", 0, null);
        orders.save(order);
        order.markFilled("2026-10-16 12:05", 1, new BigDecimal("21250"));
        orders.save(order);
        assertEquals(Order.Status.FILLED, orders.findById(order.getId()).orElseThrow().getStatus());

        Trade trade = trades.save(Trade.fill(account.getId(), symbol.getId(), order.getId(),
                "2026-10-16 12:05", Side.BUY, new BigDecimal("21250"), new BigDecimal("1"),
                BigDecimal.ZERO, new BigDecimal("0.50")));
        assertEquals(1, trades.findByAccountIdOrderByBarTimeAscIdAsc(account.getId()).size());
        // 手续费是成交那一刻的快照，不是"读 symbol 现算" —— 净盈亏 = 毛利 − 手续费
        assertEquals(new BigDecimal("-0.50"),
                trades.findById(trade.getId()).orElseThrow().netPnl());

        // ---- 语音 ----
        audioClips.save(AudioClip.record(account.getId(), symbol.getId(), "2026-10-16 12:00", 0,
                account.getId() + "/voice-0.wav", 3200, 65536, Actor.HUMAN));
        assertEquals(1, audioClips.countByAccountIdAndBarTime(account.getId(), "2026-10-16 12:00"));

        // ---- 操作记录：账户内 seq 连续 ----
        auditEvents.save(AuditEvent.of(account.getId(), 0, Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{\"cursor\":1}"));
        auditEvents.save(AuditEvent.of(account.getId(), 1, Actor.HUMAN, ReplayAction.ORDER_PLACED, "{}"));
        assertEquals(1, auditEvents.findTopByAccountIdOrderBySeqDesc(account.getId()).orElseThrow().getSeq());

        // 枚举字段落库的是 **Java 常量名**（@Enumerated(STRING)），不是协议里的小写串。
        // 谁把 @Enumerated 换回小写线值的转换器，这里就会变成 bar_advanced；
        // 而协议值是「常量名转小写」算出来的，属于输出侧的活，不该由持久化层承担。
        assertEquals(ReplayAction.ORDER_PLACED,
                auditEvents.findTopByAccountIdOrderBySeqDesc(account.getId()).orElseThrow().getAction());
        assertEquals("BAR_ADVANCED", jdbc.queryForObject(
                "select action from audit_event where account_id = ? and seq = 0", String.class, account.getId()));
        assertEquals("HUMAN", jdbc.queryForObject(
                "select actor from audit_event where account_id = ? and seq = 0", String.class, account.getId()));

        // ---- 策略 + 机器人 ----
        Strategy strategy = strategies.save(Strategy.register("caveman", "Caveman", "野人", "{}"));
        StrategyBot bot = bots.save(StrategyBot.start(account.getId(), symbol.getId(), strategy.getId(),
                new BigDecimal("1"), 300));
        bot.stop(StrategyBot.Status.FINISHED, StrategyBot.StopReason.DATA_END, 4);
        bots.save(bot);
        assertEquals(4, bots.findByAccountIdOrderByStartedAtDesc(account.getId()).get(0).getEntryCount());

        // 模板改了，账户上的规则快照不能跟着变
        template.setName("Apex 50K（改名）");
        template.getRules().setMaxDrawdown(new BigDecimal("9999.00"));
        templates.save(template);
        assertEquals(new BigDecimal("2000.00"),
                accounts.findById(account.getId()).orElseThrow().getRules().getMaxDrawdown(),
                "模板改了，账户上的规则快照不能跟着变");
        // 手数分档也必须各自独立（深拷贝），不能共享同一个列表
        assertEquals(40, accounts.findById(account.getId()).orElseThrow()
                .getRules().getPositionTiers().get(0).maxContracts());

        // ---- 交易记录 + 步骤 + 标签 ----
        TradeRecord record = TradeRecord.open(account.getId(), symbol.getId(), "2026-10-16 12:00",
                Actor.HUMAN, "jiang");
        record.openPosition(Side.BUY, "2026-10-16 12:05", new BigDecimal("21250"), new BigDecimal("1"));
        record.closeAt("2026-10-16 13:00", new BigDecimal("21260"), new BigDecimal("20"));
        record.setTags(List.of(new RecordTag("追单", "#ef4444")));
        records.save(record);

        steps.save(TradeRecordStep.of(record.getId(), 0, ReplayAction.BASE, "2026-10-16 12:00", 0,
                Actor.HUMAN, null, "{\"events\":[]}"));
        steps.save(TradeRecordStep.of(record.getId(), 1, ReplayAction.ORDER_FILLED, "2026-10-16 12:05", 1,
                Actor.HUMAN, null, "{\"events\":[]}"));

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
