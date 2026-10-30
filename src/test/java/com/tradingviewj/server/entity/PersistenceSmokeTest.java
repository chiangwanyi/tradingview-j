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
import static org.junit.jupiter.api.Assertions.assertFalse;
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
        PropFirmAccount account = accounts.save(PropFirmAccount.open(template, "第一次考核",
                "2026-10-16 09:00", "2026-10-16.csv"));

        PropFirmAccount reloadedAccount = accounts.findById(account.getId()).orElseThrow();
        assertEquals(new BigDecimal("50000.00"), reloadedAccount.getBalance());
        assertEquals(new BigDecimal("50000.00"), reloadedAccount.getBaseBalance(),
                "阶段起点余额必须单独存：升级时它会被重置，余额就不能再由成交累加反推");
        assertEquals(PropFirmAccount.Status.ACTIVE, reloadedAccount.getStatus());
        assertEquals(PropFirmStage.EVAL, reloadedAccount.getStage());
        assertEquals(new BigDecimal("52100.00"), reloadedAccount.getRules().getTrailingLockLevel());
        // 起点必须真的落库：它是「模拟时间的零点」，日历首月 / 可交易日下界 / 回放起点全靠它。
        // 和 startedAt 是两回事 —— 后者是真实时钟（这行什么时候写进库）。
        // 起点只有「时间」这一个键：曾经还有一个 bar_index，已删除（位置会被内容悄悄改变）。
        assertEquals("2026-10-16 09:00", reloadedAccount.getStartBarTime());
        assertEquals("2026-10-16.csv", reloadedAccount.getStartDayFile());
        assertFalse(columnNames("prop_firm_account").contains("start_bar_index"),
                "起点不该有 bar_index：下标是位置，文件内容一变就指向另一根 K 线");
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
        // 注意没有账户参数：图形挂在 K 线上，不挂账户
        Drawing drawing = Drawing.of(symbol.getId(), Drawing.Kind.FIB,
                new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0),
                new Anchor("2026-10-16 15:00", new BigDecimal("21300.5"), 40),
                "jiang");
        drawing.setStyle(new DrawingStyle("#ef4444", new BigDecimal("1.5"),
                DrawingStyle.Dash.SOLID, "#ef4444", new BigDecimal("0.08")));
        drawing.setFibLevels(List.of(
                new FibLevel(new BigDecimal("0"), "#888888"),
                new FibLevel(new BigDecimal("0.618"), "#f59e0b")));
        drawings.save(drawing);

        Drawing reloadedDrawing = drawings.findBySymbolId(symbol.getId()).get(0);
        assertEquals(new BigDecimal("21300.5"), reloadedDrawing.getEnd().getPrice());
        assertEquals(40, reloadedDrawing.getEnd().getOffset());
        assertEquals(2, reloadedDrawing.getFibLevels().size());
        assertEquals(new BigDecimal("0.618"), reloadedDrawing.getFibLevels().get(1).getRatio());

        // 这两张表**没有 account_id 列**：图形和语音的附体是 K 线，不参加账户统计。
        // 谁把这一列加回去，这里会当场红 —— 加了它，「换个账户同一张图」的线就消失了。
        assertFalse(columnNames("drawing").contains("account_id"), "drawing 不该有 account_id");
        assertFalse(columnNames("audio_clip").contains("account_id"), "audio_clip 不该有 account_id");
        assertTrue(columnNames("drawing").contains("symbol_id"));
        assertTrue(columnNames("audio_clip").contains("symbol_id"));

        // ---- 挂单 + 成交 ----
        Order order = Order.place(account.getId(), symbol.getId(), Side.BUY, Order.Type.LIMIT,
                new BigDecimal("21250"), new BigDecimal("1"), "2026-10-16 12:00", null);
        orders.save(order);
        order.markFilled("2026-10-16 12:05", new BigDecimal("21250"));
        orders.save(order);
        assertEquals(Order.Status.FILLED, orders.findById(order.getId()).orElseThrow().getStatus());

        // 市价单走的是同一条状态机：place 出来是 OPEN，紧接着 markFilled。
        // 曾经 place 就把它置成 FILLED，于是 markFilled 会因为「已经不是 OPEN」抛异常，
        // 成交价和成交下标只能靠调用方直接调 setter —— 状态机凭空多出一条谁也不管的路。
        Order market = Order.place(account.getId(), symbol.getId(), Side.SELL, Order.Type.MARKET,
                new BigDecimal("21260"), BigDecimal.ONE, "2026-10-16 12:10", null);
        assertEquals(Order.Status.OPEN, market.getStatus(), "下单那一刻还是 OPEN，市价单也一样");
        market.markFilled("2026-10-16 12:10", new BigDecimal("21260"));
        orders.save(market);
        assertEquals(Order.Status.FILLED, orders.findById(market.getId()).orElseThrow().getStatus());
        assertEquals(new BigDecimal("21260"), orders.findById(market.getId()).orElseThrow().getFillPrice());

        // 「哪一根」只有时间一个答案。下标会被文件内容变化悄悄改变（重抓 / 补一根 / 删坏行），
        // 而且只在某个标的下成立 —— 账户不绑标的，同一下标在两张图上就是两根 K 线。
        assertFalse(columnNames("trade_order").contains("placed_index"), "trade_order 不该有 placed_index");
        assertFalse(columnNames("trade_order").contains("filled_index"), "trade_order 不该有 filled_index");
        // 「没成交」由 filled_at_bar 为空表达，不再需要 -1 这种哨兵值
        assertEquals("2026-10-16 12:05", orders.findById(order.getId()).orElseThrow().getFilledAtBar());

        Trade trade = trades.save(Trade.fill(account.getId(), symbol.getId(), order.getId(),
                "2026-10-16 12:05", 1, Side.BUY, new BigDecimal("21250"), new BigDecimal("1"),
                BigDecimal.ZERO, new BigDecimal("0.50")));
        assertEquals(1, trades.findByAccountIdOrderByBarTimeAscSeqAsc(account.getId()).size());
        // 流水号必须真的落库：它是「同一根 K 线上多笔成交谁先谁后」的唯一依据，
        // 丢了它就只能退回按 UUID 排 —— 那是随机的。
        assertEquals(1, trades.findById(trade.getId()).orElseThrow().getSeq());
        // 手续费是成交那一刻的快照，不是"读 symbol 现算" —— 净盈亏 = 毛利 − 手续费
        assertEquals(new BigDecimal("-0.50"),
                trades.findById(trade.getId()).orElseThrow().netPnl());

        // ---- 语音 ----
        audioClips.save(AudioClip.record(symbol.getId(), "2026-10-16 12:00", 0,
                "CME_MINI_MNQ1!/2026-10-16/12-00-0.wav", 3200, 65536, Actor.HUMAN));
        assertEquals(1, audioClips.countBySymbolIdAndBarTime(symbol.getId(), "2026-10-16 12:00"));

        // ---- 操作记录：账户内 seq 连续，且每一步都能定位到「哪根 K 线、游标第几根」----
        // seq 0 是交易的起点事件（BASE = 开仓前那一屏），之后的动作从 1 开始
        auditEvents.save(AuditEvent.base(account.getId(), symbol.getId(), "2026-10-16 12:00", 0,
                Actor.HUMAN));
        auditEvents.save(AuditEvent.of(account.getId(), symbol.getId(), "2026-10-16 12:05", 1,
                Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
        auditEvents.save(AuditEvent.of(account.getId(), symbol.getId(), "2026-10-16 12:10", 2,
                Actor.HUMAN, ReplayAction.ORDER_PLACED, "{}"));
        assertEquals(2, auditEvents.findTopByAccountIdOrderBySeqDesc(account.getId()).orElseThrow().getSeq());

        // 枚举字段落库的是 **Java 常量名**（@Enumerated(STRING)），不是协议里的小写串。
        // 谁把 @Enumerated 换回小写线值的转换器，这里就会变成 bar_advanced；
        // 而协议值是「常量名转小写」算出来的，属于输出侧的活，不该由持久化层承担。
        assertEquals(ReplayAction.ORDER_PLACED,
                auditEvents.findTopByAccountIdOrderBySeqDesc(account.getId()).orElseThrow().getAction());
        assertEquals("BAR_ADVANCED", jdbc.queryForObject(
                "select action from audit_event where account_id = ? and seq = 1", String.class, account.getId()));
        assertEquals("HUMAN", jdbc.queryForObject(
                "select actor from audit_event where account_id = ? and seq = 1", String.class, account.getId()));

        // 「定位信息」必须是真的列、真的落库 —— 在 2026-10-29 之前它们只存在于 payload_json 里，
        // 那意味着每次逐 K 回放都要把整段流水拉回内存再逐条解析 JSON。
        assertTrue(columnNames("audit_event").containsAll(List.of("symbol_id", "bar_time")),
                "audit_event 必须有 symbol_id / bar_time，否则回放和进度都只能靠解析 JSON");
        assertEquals("2026-10-16 12:10", jdbc.queryForObject(
                "select bar_time from audit_event where account_id = ? and seq = 2", String.class, account.getId()));
        // 位置类字段一律不许有：下标会被文件内容变化悄悄改变，时间不会
        assertFalse(columnNames("audit_event").contains("cursor"), "audit_event 不该有 cursor 列");

        // 区间回放：这就是逐 K 回放要读的那一段。闭区间，起点和终点都要在。
        assertEquals(3, auditEvents
                .findByAccountIdAndSeqBetweenOrderBySeqAsc(account.getId(), 0, 2).size());
        assertEquals(2, auditEvents
                .findByAccountIdAndSeqBetweenOrderBySeqAsc(account.getId(), 1, 2).size(),
                "区间是闭区间：from 那条本身要参与回放");
        assertTrue(auditEvents.findByAccountIdAndSeqBetweenOrderBySeqAsc(account.getId(), 0, 0)
                .get(0).isBase(), "起点事件必须能按 action=BASE 认出来");

        // 进度既不再单独存一张表，也不存下标：它就是「最后一条事件停在哪根 K 线」。
        // 曾经有一张 replay_cursor（表），后来有一列 cursor（下标），两者都是同一份状态的
        // 第二个来源，先后都删了。
        assertEquals("2026-10-16 12:10", auditEvents
                .findTopByAccountIdAndSymbolIdOrderBySeqDesc(account.getId(), symbol.getId())
                .orElseThrow().getBarTime(), "复盘进度 = 最后一条事件的 bar_time");
        assertTrue(columnNames("replay_cursor").isEmpty(),
                "replay_cursor 已删除：进度由最后一条事件的 bar_time 回答，不该再有这张表");

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

        // ---- 交易记录：一段事件区间 + 一份摘要 ----
        // startSeq 指向那条 BASE 事件（seq 0），endSeq 是平仓那一步（seq 2）。
        // 记录本身**不复制**流水，只引用这一段。
        TradeRecord record = TradeRecord.open(account.getId(), account.getName(), symbol.getId(),
                "2026-10-16 12:00", 0, Actor.HUMAN, "jiang");
        record.openPosition(Side.BUY, "2026-10-16 12:05", new BigDecimal("21250"), new BigDecimal("1"));
        record.closeAt("2026-10-16 13:00", new BigDecimal("21260"), new BigDecimal("20"), 2);
        record.setTags(List.of(new RecordTag("追单", "#ef4444")));
        records.save(record);

        TradeRecord reloadedRecord = records.findById(record.getId()).orElseThrow();
        assertTrue(reloadedRecord.isClosed());
        // 区间和摘要是两个独立的东西：摘要留着，所以事件行将来被清理也照样能看盈亏
        assertTrue(reloadedRecord.hasSealedRange(), "收尾之后事件区间必须封口，否则回放拿不到边界");
        assertEquals(0, reloadedRecord.getStartSeq());
        assertEquals(2, reloadedRecord.getEndSeq());
        // 账户名快照：删账户之后列表上还能显示「这笔算在谁头上」
        assertEquals("第一次考核", reloadedRecord.getAccountName());
        assertEquals(new BigDecimal("20"), reloadedRecord.getRealizedPnl());
        assertEquals(1, reloadedRecord.getTags().size());
        assertEquals(record.getId(), records.findByAccountIdOrderByEntryBarTimeAsc(account.getId()).get(0).getId());

        // 区间能真的取出事件 —— 这就是「打开一条记录、逐 K 回放」的第一步
        assertEquals(3, auditEvents.findByAccountIdAndSeqBetweenOrderBySeqAsc(account.getId(),
                reloadedRecord.getStartSeq(), reloadedRecord.getEndSeq()).size());

        // 日历只列已平仓的：未平仓那笔还没有结束时间，归到哪天都不对
        assertEquals(1, records.findByAccountIdAndClosedTrueOrderByEntryBarTimeAsc(account.getId()).size());
        // 「所有交易记录」页不按账户过滤 —— 删账户之后靠它
        assertEquals(1, records.findAllByOrderByEntryBarTimeDesc().size());

        // 成交挂到记录上（弱引用，删记录不影响成交）
        trade.setTradeRecordId(record.getId());
        trades.save(trade);
        assertEquals(1, trades.findByTradeRecordIdOrderByBarTimeAscSeqAsc(record.getId()).size());
    }

    /**
     * 表的真实列名。<strong>必须问数据库、不能看实体</strong>：实体上删掉一个字段，
     * 旧库里的列还在（{@code schema.sql} 只有 CREATE IF NOT EXISTS），只有 PRAGMA 能说出真相。
     */
    private List<String> columnNames(String table) {
        return jdbc.queryForList("select name from pragma_table_info(?)", String.class, table);
    }
}
