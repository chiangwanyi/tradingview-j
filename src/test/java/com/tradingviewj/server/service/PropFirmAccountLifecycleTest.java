package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
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
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * PF 账户的三条业务规则，用真实的 SQLite 钉住：
 *
 * <ol>
 *   <li><strong>必须先有账户才能操作</strong> —— 没有账户、账户被删、账户已结束，
 *       都拿不到「可以往上挂操作」的账户。</li>
 *   <li><strong>删账户不删数据</strong> —— 图形、挂单、成交、语音、操作记录、机器人、
 *       交易记录、指标一条都不能少，{@code account_id} 也原样保留。</li>
 *   <li><strong>阶段是一个接一个攒出来的</strong> —— 三阶段挤在同一行账户上，
 *       所以每一次切换都必须在 {@code prop_firm_stage_log} 里留一行，
 *       否则「这个 FUNDED 是考来的还是直接开的」事后无从回答。</li>
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
    @Autowired private PropFirmStageLogRepository stageLogs;
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

    private Symbol symbol;
    private PropFirmTemplate evalTemplate;
    private PropFirmTemplate fundedTemplate;

    @BeforeEach
    void setUp() {
        // 每个用例用独立的标的主键，避免共用临时库时互相干扰
        String suffix = Long.toHexString(System.nanoTime());
        symbol = symbols.save(Symbol.of("CME_MINI:MNQ-" + suffix, "MNQ-" + suffix,
                new BigDecimal("0.50")));

        // LucidFlex 50K：先有 FUNDED 模板，EVAL 才有 next 可指
        fundedTemplate = templates.save(PropFirmTemplate.define(
                "LucidFlex 50K FUNDED-" + suffix, "出金号", PropFirmStage.FUNDED, null,
                new PropFirmRules(new BigDecimal("50000.00"), null, new BigDecimal("2000.00"),
                        PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100.00"),
                        null, 5, new BigDecimal("150.00"),
                        List.of(new PositionTier(BigDecimal.ZERO, 20),
                                new PositionTier(new BigDecimal("1000"), 30),
                                new PositionTier(new BigDecimal("2000"), 40)))));

        evalTemplate = templates.save(PropFirmTemplate.define(
                "LucidFlex 50K EVAL-" + suffix, "考试", PropFirmStage.EVAL,
                fundedTemplate.getId(),
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, 40)))));
    }

    @Test
    void deletingAccountKeepsEverythingItWasCounting() {
        PropFirmAccount account = accountService.open(evalTemplate.getId(), "第一次考核");
        String accountId = account.getId();

        // 账户里干了一整套活
        Drawing drawing = drawings.save(Drawing.of(accountId, symbol.getId(), Drawing.Kind.TREND,
                new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0),
                new Anchor("2026-10-16 15:00", new BigDecimal("21300.50"), 40), "jiang"));
        Order order = orders.save(Order.place(accountId, symbol.getId(), Side.BUY, Order.Type.LIMIT,
                new BigDecimal("21250"), new BigDecimal("1"), "2026-10-16 12:00", 0, null));
        Trade trade = trades.save(Trade.fill(accountId, symbol.getId(), order.getId(), "2026-10-16 12:05",
                Side.BUY, new BigDecimal("21250"), new BigDecimal("1"), BigDecimal.ZERO,
                new BigDecimal("0.13")));
        AudioClip clip = audioClips.save(AudioClip.record(accountId, symbol.getId(), "2026-10-16 12:00", 0,
                accountId + "/voice-0.wav", 3200, 65536, Actor.HUMAN));
        auditEvents.save(AuditEvent.of(accountId, 0, Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{\"cursor\":1}"));
        Strategy strategy = strategies.save(Strategy.register("caveman-" + accountId, "Caveman", "野人", "{}"));
        StrategyBot bot = bots.save(StrategyBot.start(accountId, symbol.getId(), strategy.getId(),
                new BigDecimal("1"), 300));

        TradeRecord record = TradeRecord.open(accountId, symbol.getId(), "2026-10-16 12:00",
                Actor.HUMAN, "jiang");
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
        assertEquals(symbol.getId(), drawings.findById(drawing.getId()).orElseThrow().getSymbolId(),
                "图形还必须记着自己属于哪个品种 —— 账户已经不记得了");
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
        assertNotNull(templates.findById(evalTemplate.getId()).orElseThrow());

        // 阶段流水是「这个账户存在过」的记录，跟着保留
        assertEquals(1, stageLogs.findByAccountIdOrderBySeqAsc(accountId).size(),
                "删的是统计点，不是历史");
    }

    @Test
    void operationsRequireAnExistingActiveAccount() {
        // 从来没建过账户 → 不能动手
        BusinessException missing = assertThrows(BusinessException.class,
                () -> accountService.requireActive("不存在的账户"));
        assertTrue(missing.getMessage().contains("请先创建账户"));

        // 建了又删 → 同样不能动手
        PropFirmAccount account = accountService.open(evalTemplate.getId(), "删掉的考核");
        assertEquals(account.getId(), accountService.requireActive(account.getId()).getId());
        accountService.delete(account.getId());
        assertThrows(BusinessException.class, () -> accountService.requireActive(account.getId()));

        // 开账户本身也要有真模板
        assertThrows(BusinessException.class, () -> accountService.open("没有这个模板", "x"));
    }

    @Test
    void deletingTheSameAccountTwiceIsHarmless() {
        PropFirmAccount account = accountService.open(evalTemplate.getId(), "重复删除");
        accountService.delete(account.getId());
        accountService.delete(account.getId());
        assertTrue(accounts.findById(account.getId()).isEmpty());
    }

    /**
     * 「有的是创建的时候就是 FUNDED」：拿 FUNDED 模板直接开，流水里只有一行 CREATED。
     *
     * <p>这条用例同时钉住「不需要一个 wasPromoted 布尔值」—— 身份由流水行数决定，
     * 多一个字段只会多一个能和行数打架的真相。
     */
    @Test
    void fundedTemplateStartsDirectlyAtFunded() {
        PropFirmAccount account = accountService.open(fundedTemplate.getId(), "直接买的出金号");

        assertEquals(PropFirmStage.FUNDED, account.getStage());
        List<PropFirmStageLog> history = accountService.stageHistory(account.getId());
        assertEquals(1, history.size(), "直接开出金号只有一行流水");
        assertEquals(PropFirmStageLog.Reason.CREATED, history.get(0).getReason());
        assertEquals(PropFirmStage.FUNDED, history.get(0).getStage());
    }

    /**
     * 考过来的账户：EVAL 达标 → FUNDED。
     *
     * <p>三件事必须同时成立，缺一条就是数据错了：
     * 流水两行、余额被重置回账户规模、EVAL 的规则被换成 FUNDED 的规则（手数分档从一档变三档）。
     */
    @Test
    void promotionResetsBalanceAndKeepsHistory() {
        PropFirmAccount account = accountService.open(evalTemplate.getId(), "考过来的");
        // 先假装赚了 3000 再升级 —— 这笔钱绝不能带进 FUNDED
        account.applyNetPnl(new BigDecimal("3000"));
        accounts.save(account);

        PropFirmAccount funded = accountService.promote(account.getId(), null);

        assertEquals(PropFirmStage.FUNDED, funded.getStage());
        assertEquals(new BigDecimal("50000.00"), funded.getBalance(), "余额必须重置回账户规模");
        assertEquals(new BigDecimal("50000.00"), funded.getBaseBalance());
        assertEquals(new BigDecimal("50000.00"), funded.getEodPeakBalance(), "峰值也跟着重置");
        assertEquals(0, funded.netProfitInStage().signum(), "EVAL 赚的钱不带进 FUNDED");

        // 规则换成了下一套：FUNDED 没有盈利目标、查盈利日、手数分三档
        assertNull(funded.getRules().getProfitTarget());
        assertEquals(5, funded.getRules().getMinProfitDays());
        assertEquals(3, funded.getRules().getPositionTiers().size());

        List<PropFirmStageLog> history = accountService.stageHistory(account.getId());
        assertEquals(2, history.size(), "EVAL 一行 + FUNDED 一行");
        PropFirmStageLog evalLog = history.get(0);
        assertEquals(PropFirmStage.EVAL, evalLog.getStage());
        assertNotNull(evalLog.getEndedAt(), "EVAL 这一段必须结掉");
        assertEquals(PropFirmStageLog.EndReason.PROMOTED, evalLog.getEndReason());
        assertEquals(new BigDecimal("3000.00"), evalLog.getNetPnl(), "EVAL 的盈利留在 EVAL 那一段的流水里");
        assertEquals(PropFirmStageLog.Reason.PROMOTED, history.get(1).getReason());
        assertNull(history.get(1).getEndedAt());
    }

    /** FUNDED 攒够盈利日 → PAYOUT：账户终态，不再接受下单，但画图录音仍然允许。 */
    @Test
    void payoutIsTerminalForTradingButNotForReading() {
        PropFirmAccount account = accountService.open(fundedTemplate.getId(), "出金号");
        accountService.promote(account.getId(), null);

        PropFirmAccount done = accounts.findById(account.getId()).orElseThrow();
        assertEquals(PropFirmStage.PAYOUT, done.getStage());
        // 「走完流程」不等于「这行数据作废」：账户仍然 ACTIVE，只是不再接受下单
        assertEquals(PropFirmAccount.Status.ACTIVE, done.getStatus());

        // 看盘动作还有效
        assertEquals(account.getId(), accountService.requireActive(account.getId()).getId());
        // 下单不行
        BusinessException e = assertThrows(BusinessException.class,
                () -> accountService.requireTradable(account.getId()));
        assertTrue(e.getMessage().contains("PAYOUT"));

        List<PropFirmStageLog> history = accountService.stageHistory(account.getId());
        assertEquals(PropFirmStageLog.EndReason.PAYOUT_DONE, history.get(0).getEndReason(),
                "FUNDED 那一段是以出金结束的");
        assertEquals(PropFirmStage.PAYOUT, history.get(1).getStage());
        assertNull(history.get(1).getEndedAt(), "PAYOUT 是终点，它那一段一直开着");
    }

    /** 模板没配 next_template_id 时，升级必须当场报错，而不是悄悄把账户切成 FUNDED。 */
    @Test
    void promotionWithoutNextTemplateFailsLoudly() {
        PropFirmTemplate orphan = templates.save(PropFirmTemplate.define(
                "没有下一段的 EVAL", null, PropFirmStage.EVAL, null,
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        null, new BigDecimal("0.50"), null, null, List.of())));
        PropFirmAccount account = accountService.open(orphan.getId(), "孤儿");

        BusinessException e = assertThrows(BusinessException.class,
                () -> accountService.promote(account.getId(), null));
        assertTrue(e.getMessage().contains("next_template_id"));
        // 失败之后账户必须原样留在 EVAL
        assertEquals(PropFirmStage.EVAL, accounts.findById(account.getId()).orElseThrow().getStage());
    }
}
