package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.audit.payload.OrderFilledPayload;
import com.tradingviewj.server.entity.audit.payload.OrderPlacedPayload;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRepository;
import com.tradingviewj.server.ws.TradeDeskService;
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
import java.util.Comparator;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.when;

/**
 * 下单 / 撮合 / 成交 / 盈亏的<strong>账务侧</strong>，用真实的 SQLite（临时库）+ 临时 CSV 钉住。
 *
 * <p>每条用例都核对<strong>三个数字</strong>：成交价、手续费、净盈亏 —— 它们是「盈亏基于成交价和
 * 手续费」这句话的全部内容。持仓不单独断言一遍：它由 {@link TradeLedger} 从成交重放，
 * 重放错了会在净盈亏上先暴露。
 *
 * <p>金额一律用 {@link #assertMoney}（{@code compareTo}）比较：{@code BigDecimal.equals}
 * 会把 {@code 12} 和 {@code 12.0000000000} 判成不同 —— 那种失败和「算错了」长得一模一样。
 *
 * <p>业务库 {@code ./db/tradingview-j.db} 一次都不碰，K 线也只写临时目录 ——
 * 和 {@code PropFirmAccountServiceTest} 同一个理由：跑测试不能悄悄改用户的账户和行情。
 */
@SpringBootTest
class TradingServiceTest {

    private static final String SYMBOL = "CME_MINI:MES1!";
    private static final String FOLDER = "CME_MINI_MES1!";

    private static String testDbUrl;
    private static Path dataDir;

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            Path dir = Files.createTempDirectory("tvj-trading-").toAbsolutePath();
            testDbUrl = "jdbc:sqlite:" + dir.resolve("test.db");
            dataDir = dir.resolve("data");
            registry.add("spring.datasource.url", () -> testDbUrl);
            registry.add("app.bar-root", () -> dataDir.toString());
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @MockitoBean
    private PropFirmStartPicker picker;

    @Autowired private TradingService trading;
    @Autowired private TradeDeskService desk;
    @Autowired private PropFirmAccountService accounts;
    @Autowired private PropFirmTemplateService templates;
    @Autowired private PropFirmAccountRepository accountRepo;
    @Autowired private PropFirmTemplateRepository templateRepo;
    @Autowired private com.tradingviewj.server.repository.SymbolRepository symbolRepo;
    @Autowired private OrderRepository orderRepo;
    @Autowired private TradeRepository tradeRepo;
    @Autowired private TradeRecordRepository recordRepo;
    @Autowired private AuditEventRepository auditRepo;

    private String accountId;
    private String barTime;

    @BeforeEach
    void clean() {
        auditRepo.deleteAll();
        tradeRepo.deleteAll();   // trade.trade_record_id 指向 trade_record，先删成交
        recordRepo.deleteAll();
        orderRepo.deleteAll();
        accountRepo.deleteAll();
        templateRepo.deleteAll();
        symbolRepo.deleteAll();

        writeSymbols();
        // symbol 表也要有这一行：账户的 symbol_id 是真外键（清单文件只是「怎么读」的来源）
        symbolRepo.save(com.tradingviewj.server.entity.market.Symbol.of(
                SYMBOL, FOLDER, new BigDecimal("0.50")));
        writeDay("2024-03-04",
                "2024-03-04 09:00,4000.00,4002.00,3998.00,4001.00,10",
                "2024-03-04 09:05,4001.00,4005.00,3999.00,4004.00,12",
                "2024-03-04 09:10,4004.00,4006.00,4000.00,4002.00,9");

        when(picker.pick(anyString())).thenReturn(
                new PropFirmStartPicker.Pick(FOLDER, "2024-03-04 09:00", "2024-03-04.csv"));

        PropFirmTemplate eval = templates.create("EVAL 50K", "考试", PropFirmStage.EVAL,
                null, new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, 40))));
        PropFirmAccount account = accounts.open(eval.getId(), SYMBOL, "撮合测试账户");
        accountId = account.getId();
        barTime = account.getLastBarTime();
    }

    // ---------------- 挂单 ----------------

    /** 挂单只是「等价格过来」的意图：落一张 OPEN 单，不产生成交，也不动持仓。 */
    @Test
    void placeCreatesOpenOrderWithoutTrade() {
        TradingService.Event event = trading.place(accountId, "buy", "limit",
                new BigDecimal("3998.00"), BigDecimal.ONE);

        assertEquals("order_placed", event.action().wire());
        OrderPlacedPayload payload = (OrderPlacedPayload) event.payload();
        assertEquals(1, payload.orders().size());
        assertEquals("open", payload.orders().get(0).status());
        assertEquals("limit", payload.orders().get(0).kind());
        assertMoney("3998.00", payload.orders().get(0).price());

        assertEquals(0, tradeRepo.count(), "挂单不动成交");
        assertEquals("flat", trading.portfolio(accountId).position().side());
    }

    /**
     * 本根挂的单<strong>本根不撮合</strong>：09:00 那根的 low 是 3998，挂 3998 的买单在
     * 真实数据上「已经」触发了 —— 但它是这根收完之后才挂的，用已收完的行情倒推成交等于作弊。
     */
    @Test
    void orderPlacedOnCurrentBarDoesNotFillOnIt() {
        trading.place(accountId, "buy", "limit", new BigDecimal("3998.00"), BigDecimal.ONE);
        assertEquals(0, tradeRepo.count());
        assertEquals(Order.Status.OPEN,
                orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId).get(0).getStatus());
    }

    /** 参数校验：手数不是整数、用了 market 当挂单类型、价格非正，都要被拒且不留半条数据。 */
    @Test
    void placeRejectsBadInputAndLeavesNothing() {
        assertThrows(BusinessException.class, () -> trading.place(
                accountId, "buy", "limit", new BigDecimal("3998"), new BigDecimal("0.5")));
        assertThrows(BusinessException.class, () -> trading.place(
                accountId, "buy", "market", new BigDecimal("3998"), BigDecimal.ONE));
        assertThrows(BusinessException.class, () -> trading.place(
                accountId, "buy", "limit", BigDecimal.ZERO, BigDecimal.ONE));
        assertThrows(BusinessException.class, () -> trading.place(
                accountId, "long", "limit", new BigDecimal("3998"), BigDecimal.ONE));
        assertEquals(0, orderRepo.count());
        assertEquals(0, tradeRepo.count());
        assertEquals(0, auditRepo.count());
    }

    // ---------------- 市价单 ----------------

    /** 市价单按当前这根的收盘价立即成交，手续费按成交那一刻的费率快照进 trade。 */
    @Test
    void marketOrderFillsAtCurrentCloseAndChargesCommission() {
        TradingService.Event event = trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        assertEquals("order_filled", event.action().wire());
        OrderFilledPayload payload = (OrderFilledPayload) event.payload();

        assertEquals("filled", payload.order().status());
        assertEquals("market", payload.order().kind());
        assertMoney("4001.00", payload.order().fillPrice());
        assertMoney("4001.00", payload.trade().price());
        assertMoney("1.00", payload.trade().commission()); // 2 手 × 0.5/手/边
        assertMoney("0", payload.trade().realizedPnl());   // 开仓不产生已实现盈亏
        assertMoney("-1.00", payload.trade().netPnl());    // 开仓那一笔只有成本

        assertEquals("long", payload.position().side());
        assertMoney("2", payload.position().quantity());
        assertMoney("4001.00", payload.position().averagePrice());
        assertMoney("-1.00", payload.realizedPnl());       // 净额 = 0 − 1.00 手续费
        assertMoney("0", payload.unrealizedPnl());         // 现价就是成交价
    }

    /** nATR 定手数：市价成交之后，同一个事务里挂一张反方向的止损单（价格 = 成交价 ∓ 距离）。 */
    @Test
    void marketOrderWithStopDistanceHangsProtection() {
        OrderFilledPayload payload = (OrderFilledPayload) trading
                .market(accountId, "buy", BigDecimal.ONE, new BigDecimal("3.00")).payload();

        assertNotNull(payload.stopOrder(), "带距离就得挂上保护性止损");
        assertEquals("stop", payload.stopOrder().kind());
        assertEquals("sell", payload.stopOrder().side());
        assertMoney("3998.00", payload.stopOrder().price()); // 成交价 4001 − 3
        assertEquals("open", payload.stopOrder().status());
        assertEquals(2, payload.orders().size(), "市价单（已成交）+ 止损单都在全量里");
    }

    /** 止损价算出来非正：整笔市价单不提交 —— 先开仓再发现挂不上就只能靠再平一次补救。 */
    @Test
    void marketOrderRejectsWhenStopPriceWouldBeNonPositive() {
        BusinessException e = assertThrows(BusinessException.class, () -> trading
                .market(accountId, "buy", BigDecimal.ONE, new BigDecimal("9999.00")));
        assertTrue(e.getMessage().contains("这笔市价单没有提交"));
        assertEquals(0, orderRepo.count(), "不能留下一个裸仓");
        assertEquals(0, tradeRepo.count());
    }

    // ---------------- 撮合（推进一根） ----------------

    /**
     * 撮合发生在【下一根】：触发判定 + 跳空修正 + 成交记账，全部并进那条 {@code bar_advanced}。
     *
     * <p>这里用限价买 4003：09:05 的 low 3999 会触发它，但开盘 4001 比挂单价更「好」——
     * 成交价取 min(挂单价, 开盘价) = 4001，不是 4003。
     */
    @Test
    void stepMatchesLimitOrderWithGapCorrection() {
        trading.place(accountId, "buy", "limit", new BigDecimal("4003.00"), BigDecimal.valueOf(2));
        assertEquals(0, tradeRepo.count(), "本根不撮合");

        TradingService.Event event = desk.step(accountId);
        assertEquals("bar_advanced", event.action().wire());
        assertEquals(1, tradeRepo.count(), "下一根触发了");

        Trade trade = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId).get(0);
        assertEquals("2024-03-04 09:05", trade.getBarTime());
        assertMoney("4001.00", trade.getPrice()); // 跳空修正：开盘更低，按开盘成交
        assertEquals(1, trade.getSeq(), "成交流水号从 1 起");
        assertMoney("1.00", trade.getCommission());

        TradingService.Portfolio book = trading.portfolio(accountId);
        assertMoney("-1.00", book.realizedPnl());
        assertMoney("2", book.position().quantity());
    }

    /**
     * 往返一笔：买 2 手 @4001、下一根卖 2 手 @4004。
     *
     * <p>毛利 = (4004 − 4001) × 2 手 × 每点 2 美元 = 12；手续费 = 4 × 0.5 = 2（买卖各一次）；
     * 净额 = 10。开仓那一笔的 netPnl 是 −1（只有成本），平仓那一笔是 +11。
     */
    @Test
    void roundTripPnlIsNetOfFillPriceAndCommission() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        desk.step(accountId);
        trading.market(accountId, "sell", BigDecimal.valueOf(2), null);

        List<Trade> trades = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId);
        assertEquals(2, trades.size());
        assertMoney("4001.00", trades.get(0).getPrice());
        assertMoney("4004.00", trades.get(1).getPrice());
        assertMoney("12", trades.get(1).getRealizedPnl());  // 毛利 3 点 × 2 手 × 2 美元
        assertMoney("1.00", trades.get(1).getCommission());
        assertMoney("11", trades.get(1).netPnl());
        assertMoney("-1.00", trades.get(0).netPnl());

        TradingService.Portfolio book = trading.portfolio(accountId);
        assertMoney("10", book.realizedPnl());              // 净已实现 = 12 − 2
        assertEquals("flat", book.position().side());
        assertEquals(1, book.closedLots().size());
        assertMoney("12", book.closedLots().get(0).realizedPnl()); // 逐批归因按这一批自己的开仓价
    }

    // ---------------- 交易记录的开 / 闭 ----------------

    /**
     * 空仓 → 有仓开记录，仓位回到 0 封口。<strong>归零不新开一条</strong>。
     *
     * <p>{@code realizedPnl} 记的是<strong>毛利</strong>（Σ 每笔成交的 {@code trade.realizedPnl}），
     * 手续费不在这个字段里 —— 它和 {@code Trade} 是一个口径，净额仍然是
     * {@code TradingService.Portfolio.realizedPnl} 的职责。
     */
    @Test
    void recordOpensOnFirstFillAndSealsAtFlat() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        List<TradeRecord> afterOpen = recordRepo.findAll();
        assertEquals(1, afterOpen.size(), "开仓就有一条未闭合记录");
        TradeRecord rec = afterOpen.get(0);
        assertFalse(rec.isClosed());
        assertEquals(1, rec.getStartSeq(), "起点是开仓那一笔成交的 seq");
        assertEquals(-1, rec.getEndSeq());
        assertEquals(Side.BUY, rec.getSide());
        assertMoney("2", rec.getQuantity());
        assertMoney("4001.00", rec.getEntryPrice());
        assertEquals(1, rec.getFillCount());

        desk.step(accountId);
        trading.market(accountId, "sell", BigDecimal.valueOf(2), null);

        TradeRecord sealed = recordRepo.findById(rec.getId()).orElseThrow();
        assertTrue(sealed.isClosed());
        assertEquals(2, sealed.getEndSeq(), "终点是平仓那一笔成交的 seq");
        assertMoney("4004.00", sealed.getExitPrice());
        assertMoney("12", sealed.getRealizedPnl());
        assertEquals(2, sealed.getFillCount());
        assertEquals(1, recordRepo.count(), "归零之后不开新记录");
    }

    /** 一键退仓的平仓是同一笔反向成交，所以封口走的是同一条判据，不需要第二条分支。 */
    @Test
    void flatAllSealsTheRecord() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        trading.flatAll(accountId);

        assertEquals(1, recordRepo.count());
        assertTrue(recordRepo.findAll().get(0).isClosed());
        assertTrue(recordRepo
                .findFirstByAccountIdAndClosedFalseOrderByStartSeqAsc(accountId).isEmpty());
    }

    /** 同向的加仓和部分平仓都<strong>不</strong>断记录：手数跟着走，峰值单独记。 */
    @Test
    void recordFollowsQuantityWhileDirectionUnchanged() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        trading.market(accountId, "buy", BigDecimal.ONE, null);
        desk.step(accountId);
        trading.market(accountId, "sell", BigDecimal.ONE, null);

        assertEquals(1, recordRepo.count(), "同向加减仓不开新记录");
        TradeRecord rec = recordRepo.findAll().get(0);
        assertFalse(rec.isClosed());
        assertMoney("2", rec.getQuantity(), "3 手减掉 1 手");
        assertMoney("3", rec.getMaxQuantity(), "峰值是 3 手，不跟着回头");
        assertEquals(3, rec.getFillCount(), "三次成交：开 2、加 1、平 1");
    }

    /**
     * 反手：穿零是<strong>中间态</strong>，一笔成交要闭旧开新，两条记录共享这一个 seq。
     *
     * <p>买 2 @4001 → 卖 5 @4004，净 −3。旧记录 {start 1, end 2}、盈亏 (4004−4001)×2×2 = 12
     * 只算平掉的那 2 手；新记录从 seq 2 起，做空 3 手 @4004。
     */
    @Test
    void reversalSealsOldRecordAndOpensNewOnTheSameFill() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        desk.step(accountId);
        trading.market(accountId, "sell", BigDecimal.valueOf(5), null);

        List<TradeRecord> all = recordRepo.findAll().stream()
                .sorted(Comparator.comparingInt(TradeRecord::getStartSeq)).toList();
        assertEquals(2, all.size(), "反手是两条记录，不是一条");

        TradeRecord old = all.get(0);
        assertTrue(old.isClosed());
        assertEquals(1, old.getStartSeq());
        assertEquals(2, old.getEndSeq());
        assertMoney("12", old.getRealizedPnl(), "只有平掉的 2 手产生盈亏");

        TradeRecord fresh = all.get(1);
        assertFalse(fresh.isClosed());
        assertEquals(2, fresh.getStartSeq(), "新记录与旧记录共享这笔成交的 seq");
        assertEquals(-1, fresh.getEndSeq());
        assertEquals(Side.SELL, fresh.getSide());
        assertMoney("3", fresh.getQuantity());
        assertMoney("4004.00", fresh.getEntryPrice());
        assertEquals(1, fresh.getFillCount());
    }

    /**
     * OCO：同组的止损和限价在同一根上都被触发时，止损赢、限价自动撤销。
     *
     * <p>不拦的话，一张限价成交平完仓，止损单接着成交，会平过头、开出一个反向仓。
     * 组号没有对外接口（手动下单永远是独立单），所以这里用 {@code Order.place} 直接建两张带组的单。
     */
    @Test
    void ocoFillsOnlyTheStopAndCancelsTheSibling() {
        Order limit = Order.place(accountId, SYMBOL, Side.SELL, Order.Type.LIMIT,
                new BigDecimal("4004.00"), BigDecimal.ONE, barTime, "g1");
        Order stop = Order.place(accountId, SYMBOL, Side.SELL, Order.Type.STOP,
                new BigDecimal("3999.00"), BigDecimal.ONE, barTime, "g1");
        orderRepo.save(limit);
        orderRepo.save(stop);

        desk.step(accountId);

        assertEquals(1, tradeRepo.count(), "一组只成交一张");
        assertEquals(Order.Status.FILLED, orderRepo.findById(stop.getId()).orElseThrow().getStatus(),
                "止损优先（对持仓更不利）");
        assertEquals(Order.Status.CANCELLED, orderRepo.findById(limit.getId()).orElseThrow().getStatus(),
                "同组另一张自动撤销");
    }

    // ---------------- 撤单 / 改价 / 退仓 ----------------

    @Test
    void cancelOpenOrderAndRejectCancellingAFilledOne() {
        trading.place(accountId, "buy", "limit", new BigDecimal("3998.00"), BigDecimal.ONE);
        String orderId = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId).get(0).getId();

        TradingService.Event event = trading.cancel(accountId, orderId);
        assertEquals("order_cancelled", event.action().wire());
        assertEquals(Order.Status.CANCELLED, orderRepo.findById(orderId).orElseThrow().getStatus());

        // 再撤一次：已经撤销了
        assertThrows(BusinessException.class, () -> trading.cancel(accountId, orderId));

        // 已成交的撤不掉
        trading.market(accountId, "buy", BigDecimal.ONE, null);
        String filledId = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId).get(0).getOrderId();
        BusinessException e = assertThrows(BusinessException.class, () -> trading.cancel(accountId, filledId));
        assertTrue(e.getMessage().contains("已经成交"));
    }

    @Test
    void moveChangesPriceAndRejectsNoOp() {
        trading.place(accountId, "buy", "limit", new BigDecimal("3998.00"), BigDecimal.ONE);
        String orderId = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId).get(0).getId();

        trading.move(accountId, orderId, new BigDecimal("3997.75"));
        assertMoney("3997.75", orderRepo.findById(orderId).orElseThrow().getPrice());
        assertMoney("3997.75", trading.portfolio(accountId).orders().get(0).price());

        assertThrows(BusinessException.class,
                () -> trading.move(accountId, orderId, new BigDecimal("3997.75")), "价格没变不该写库");
        assertThrows(BusinessException.class,
                () -> trading.move(accountId, orderId, new BigDecimal("0")), "价格必须为正");
    }

    /**
     * 一键退仓：<strong>先平后撤</strong>。平仓走的是和市价单完全同一条路，
     * 所以「一键退仓」和「手动平仓」算出来的钱不可能不一样。
     */
    @Test
    void flatAllClosesPositionThenCancelsOrders() {
        trading.market(accountId, "buy", BigDecimal.valueOf(2), null);
        desk.step(accountId); // 09:05，收盘 4004
        trading.place(accountId, "sell", "limit", new BigDecimal("4100.00"), BigDecimal.ONE);

        TradingService.Event event = trading.flatAll(accountId);
        assertEquals("flat_all", event.action().wire());

        TradingService.Portfolio book = trading.portfolio(accountId);
        assertEquals("flat", book.position().side());
        assertEquals(0, book.orders().stream().filter(o -> o.status().equals("open")).count(),
                "挂单全部撤销");
        assertEquals(2, book.trades().size(), "平仓是一笔新的成交，不是撤单");
        assertMoney("4004.00", book.trades().get(1).price());
        assertMoney("12", book.trades().get(1).realizedPnl());
        assertMoney("10", book.realizedPnl(), "12 − 2 手续费");
    }

    /** 空仓 + 没挂单：报错而不是静默成功 —— 否则「按钮没反应」和「真的没事可做」分不清。 */
    @Test
    void flatAllWithNothingToDoIsAnError() {
        BusinessException e = assertThrows(BusinessException.class, () -> trading.flatAll(accountId));
        assertTrue(e.getMessage().contains("不用退仓"));
    }

    /** 查询视图：订单与成交按时间排好、字段和协议一致（小写方向、毛利/手续费/净额齐全）。 */
    @Test
    void queryViewMatchesWireShape() {
        trading.market(accountId, "buy", BigDecimal.ONE, null);
        TradingService.Portfolio book = trading.portfolio(accountId);

        assertEquals(1, book.orders().size());
        assertEquals("buy", book.orders().get(0).side());
        assertEquals("filled", book.orders().get(0).status());
        assertNotNull(book.orders().get(0).filledAt());
        assertEquals("2024-03-04 09:00", book.orders().get(0).placedAt());

        assertEquals(1, book.trades().size());
        assertMoney("0.50", book.trades().get(0).commission());
        assertMoney("-0.50", book.trades().get(0).netPnl());
        assertMoney("0", book.trades().get(0).realizedPnl());
    }

    // ---------------- 夹具 ----------------

    /**
     * 金额断言：按 {@code compareTo} 比，忽略 scale。
     *
     * <p>{@code BigDecimal.equals} 会把 {@code 12} 和 {@code 12.0000000000} 判成不同 ——
     * 而后者正是 {@code TradeLedger} 里 MathContext 除法的正常产物。那种失败长得和「算错了」一模一样。
     */
    private static void assertMoney(String expected, BigDecimal actual) {
        assertNotNull(actual, "金额不该是 null（期望 " + expected + "）");
        assertEquals(0, new BigDecimal(expected).compareTo(actual),
                "金额不等：期望 " + expected + "，实际 " + actual);
    }

    private static void assertMoney(String expected, BigDecimal actual, String message) {
        assertNotNull(actual, message + "（金额不该是 null，期望 " + expected + "）");
        assertEquals(0, new BigDecimal(expected).compareTo(actual),
                message + "：期望 " + expected + "，实际 " + actual);
    }

    private void writeSymbols() {
        try {
            Files.createDirectories(dataDir);
            Files.writeString(dataDir.resolve("_symbols.json"), """
                    {"version":1,"symbols":{"CME_MINI:MES1!":{
                      "folder":"CME_MINI_MES1!","tick":0.25,"tickValue":0.5,"commission":0.5}}}
                    """);
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    private void writeDay(String day, String... rows) {
        try {
            Path dir = dataDir.resolve(FOLDER);
            Files.createDirectories(dir);
            Files.write(dir.resolve(day + ".csv"), List.of(rows));
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }
}
