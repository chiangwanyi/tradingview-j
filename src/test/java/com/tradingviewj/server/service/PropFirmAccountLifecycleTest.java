package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.indicator.Indicator;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.record.TradeRecordStep;
import com.tradingviewj.server.entity.strategy.Strategy;
import com.tradingviewj.server.entity.strategy.StrategyBot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AudioClipRepository;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.DrawingRepository;
import com.tradingviewj.server.repository.IndicatorRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.StrategyBotRepository;
import com.tradingviewj.server.repository.StrategyRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRecordStepRepository;
import com.tradingviewj.server.repository.TradeRepository;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * PF 账户的两条业务规则，用真实的 SQLite 钉住：
 *
 * <ol>
 *   <li><strong>必须先有账户才能操作</strong> —— 没有账户、账户被删、账户已结束，
 *       都拿不到「可以往上挂操作」的账户。</li>
 *   <li><strong>删账户不删数据</strong> —— 图形、挂单、成交、语音、操作记录、机器人、
 *       交易记录、指标一条都不能少，{@code account_id} 也原样保留。</li>
 * </ol>
 *
 * <p>为什么必须真连库：删账户走的是 JPA delete + 子表没有外键这一整套真实行为，
 * 用 mock 测等于什么都没测 —— mock 永远会返回「没删」。
 */
@SpringBootTest
class PropFirmAccountLifecycleTest {

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-lifecycle-").toAbsolutePath().toString();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir + "/test.db");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private PropFirmAccountService accountService;
    @Autowired private PropFirmAccountRepository accounts;
    @Autowired private PropFirmTemplateRepository templates;
    @Autowired private SymbolRepository symbols;
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

    private Symbol symbol;
    private PropFirmTemplate template;

    @BeforeEach
    void setUp() {
        // 每个用例用独立的标的主键，避免共用临时库时互相干扰
        String suffix = Long.toHexString(System.nanoTime());
        symbol = symbols.save(Symbol.of("CME_MINI:MNQ-" + suffix, "MNQ-" + suffix));
        template = templates.save(PropFirmTemplate.define("Apex 50K", "50K 考核",
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2500.00"), new BigDecimal("1250.00"),
                        PropFirmRules.DrawdownType.TRAILING_EOD, 5, new BigDecimal("0.90"))));
    }

    @Test
    void deletingAccountKeepsEverythingItWasCounting() {
        PropFirmAccount account = accountService.open(template.getId(), "第一次考核", symbol.getId());
        String accountId = account.getId();

        // 账户里干了一整套活
        Indicator indicator = indicators.save(
                Indicator.of(accountId, Indicator.Type.EMA, 20, Indicator.Source.CLOSE));
        Drawing drawing = drawings.save(Drawing.of(accountId, Drawing.Kind.TREND,
                new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0),
                new Anchor("2026-10-16 15:00", new BigDecimal("21300.50"), 40), "jiang"));
        Order order = orders.save(Order.place(accountId, Side.BUY, Order.Type.LIMIT,
                new BigDecimal("21250"), new BigDecimal("1"), "2026-10-16 12:00", 0, null));
        Trade trade = trades.save(Trade.fill(accountId, order.getId(), "2026-10-16 12:05",
                Side.BUY, new BigDecimal("21250"), new BigDecimal("1"), BigDecimal.ZERO));
        AudioClip clip = audioClips.save(AudioClip.record(accountId, "2026-10-16 12:00", 0,
                accountId + "/voice-0.wav", 3200, 65536, Actor.HUMAN));
        auditEvents.save(AuditEvent.of(accountId, 0, Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{\"cursor\":1}"));
        Strategy strategy = strategies.save(Strategy.register("caveman-" + accountId, "Caveman", "野人", "{}"));
        StrategyBot bot = bots.save(StrategyBot.start(accountId, strategy.getId(), new BigDecimal("1"), 300));

        TradeRecord record = TradeRecord.open(accountId, "2026-10-16 12:00", Actor.HUMAN, "jiang");
        record.openPosition(Side.BUY, "2026-10-16 12:05", new BigDecimal("21250"), new BigDecimal("1"));
        record.closeAt("2026-10-16 13:00", new BigDecimal("21260"), new BigDecimal("20"));
        records.save(record);
        steps.save(TradeRecordStep.of(record.getId(), 0, ReplayAction.BASE, "2026-10-16 12:00", 0,
                Actor.HUMAN, null, "{\"events\":[]}"));

        // 删账户
        accountService.delete(accountId);

        // 账户没了
        assertTrue(accounts.findById(accountId).isEmpty(), "账户这一行应该被删掉");

        // 但账上的东西一件都不少，而且仍然记着原来那个 account_id（不置空、不改写）
        assertEquals(accountId, drawings.findById(drawing.getId()).orElseThrow().getAccountId(),
                "图形必须保留，并且保留原账户 id");
        assertEquals(accountId, indicators.findById(indicator.getId()).orElseThrow().getAccountId());
        assertEquals(accountId, orders.findById(order.getId()).orElseThrow().getAccountId());
        assertEquals(accountId, trades.findById(trade.getId()).orElseThrow().getAccountId());
        assertEquals(accountId, audioClips.findById(clip.getId()).orElseThrow().getAccountId());
        assertEquals(accountId, bots.findById(bot.getId()).orElseThrow().getAccountId());

        TradeRecord keptRecord = records.findById(record.getId()).orElseThrow();
        assertEquals(accountId, keptRecord.getAccountId(), "交易记录必须保留，并且保留原账户 id");
        assertEquals(new BigDecimal("20"), keptRecord.getRealizedPnl(), "记录内容一位都不能变");
        assertEquals(1, steps.findByRecordIdOrderByStepNoAsc(record.getId()).size(), "步骤属于记录，记录还在步骤就在");

        // 操作记录同样保留原账户 id —— 它是 append-only 的流水，任何改写都等于伪造
        assertEquals(1, auditEvents.findByAccountIdOrderBySeqAsc(accountId).size(),
                "操作记录必须保留原账户 id");

        // 模板也不受影响（账户存的是规则副本）
        assertNotNull(templates.findById(template.getId()).orElseThrow());
    }

    @Test
    void operationsRequireAnExistingActiveAccount() {
        // 从来没建过账户 → 不能动手
        BusinessException missing = assertThrows(BusinessException.class,
                () -> accountService.requireActive("不存在的账户"));
        assertTrue(missing.getMessage().contains("请先创建账户"));

        // 建了又删 → 同样不能动手
        PropFirmAccount account = accountService.open(template.getId(), "删掉的考核", symbol.getId());
        assertEquals(account.getId(), accountService.requireActive(account.getId()).getId());
        accountService.delete(account.getId());
        assertThrows(BusinessException.class, () -> accountService.requireActive(account.getId()));

        // 开账户本身也要有真模板、真标的
        assertThrows(BusinessException.class, () -> accountService.open("没有这个模板", "x", symbol.getId()));
        assertThrows(BusinessException.class, () -> accountService.open(template.getId(), "x", "没有这个标的"));
    }

    @Test
    void deletingTheSameAccountTwiceIsHarmless() {
        PropFirmAccount account = accountService.open(template.getId(), "重复删除", symbol.getId());
        accountService.delete(account.getId());
        accountService.delete(account.getId());
        assertTrue(accounts.findById(account.getId()).isEmpty());
    }
}
