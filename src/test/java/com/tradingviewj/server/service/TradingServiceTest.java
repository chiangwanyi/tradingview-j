package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.audit.payload.BarAdvancedPayload;
import com.tradingviewj.server.entity.audit.payload.OrderFilledPayload;
import com.tradingviewj.server.entity.audit.payload.OrderPlacedPayload;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.TradeOrder;
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
import static org.junit.jupiter.api.Assertions.assertNull;
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

        clearBars();
        writeSymbols();
        // symbol 表也要有这一行：账户的 symbol_id 是真外键（清单文件只是「怎么读」的来源）
        // 刻度必须和清单一一致：考核判定按 symbol 表的 tick/tickValue 算每点价值
        com.tradingviewj.server.entity.market.Symbol mes = com.tradingviewj.server.entity.market.Symbol.of(
                SYMBOL, FOLDER, new BigDecimal("0.50"));
        mes.setTick(new BigDecimal("0.25"));
        mes.setTickValue(new BigDecimal("0.5"));
        symbolRepo.save(mes);
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
        assertEquals(TradeOrder.Status.OPEN,
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

    // ---------------- 手数限制 ----------------

    /** 超过档位的市价单当场拒绝：不留挂单、不留成交 —— 否则「超限」就只是句提示。 */
    @Test
    void marketOrderAboveTierLimitIsRejected() {
        String capped = openAccountWithLimit(1);

        BusinessException e = assertThrows(BusinessException.class,
                () -> trading.market(capped, "buy", BigDecimal.valueOf(2), null));
        assertTrue(e.getMessage().contains("手数上限"), e.getMessage());
        assertEquals(0, orderRepo.count());
        assertEquals(0, tradeRepo.count());
    }

    /** 挂单也过同一道闸：将来会成交的仓位数现在就得合规，不能等到撮合那根才发现。 */
    @Test
    void placeAboveTierLimitIsRejected() {
        String capped = openAccountWithLimit(1);

        assertThrows(BusinessException.class, () -> trading.place(capped, "buy", "limit",
                new BigDecimal("4001.00"), BigDecimal.valueOf(2)));
        assertEquals(0, orderRepo.count());
    }

    /**
     * 两张各自合规（1 手 + 1 手）的挂单在同一根里都触发：档位只有 1 手，
     * 所以<strong>只有第一张成交</strong>，第二张不动、单子留 OPEN；等仓位降下来它才成交。
     */
    @Test
    void stackedLimitOrdersStopAtTierLimit() {
        String capped = openAccountWithLimit(1);
        trading.place(capped, "buy", "limit", new BigDecimal("4010.00"), BigDecimal.ONE);
        trading.place(capped, "buy", "limit", new BigDecimal("4010.00"), BigDecimal.ONE);

        desk.step(capped); // 09:05：两张都触发，但只能成交 1 手
        assertEquals(1, tradeRepo.count(), "第二张会超限，不成交");
        List<TradeOrder> first = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(capped);
        assertEquals(TradeOrder.Status.FILLED, first.get(0).getStatus());
        assertEquals(TradeOrder.Status.OPEN, first.get(1).getStatus(), "超限的单留着，不撤也不丢");

        // 平掉之后仓位归零，那张留下的单在下一根就能成交
        trading.market(capped, "sell", BigDecimal.ONE, null);
        desk.step(capped);
        assertEquals(3, tradeRepo.count(), "开 1 + 平 1 + 那张留下的单终于成交 1");
        List<TradeOrder> after = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(capped);
        assertEquals(TradeOrder.Status.FILLED, after.get(1).getStatus());
    }



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

        desk.step(accountId);
        trading.market(accountId, "sell", BigDecimal.valueOf(2), null);

        TradeRecord sealed = recordRepo.findById(rec.getId()).orElseThrow();
        assertTrue(sealed.isClosed());
        assertEquals(2, sealed.getEndSeq(), "终点是平仓那一笔成交的 seq");
        assertMoney("4004.00", sealed.getExitPrice());
        assertMoney("12", sealed.getRealizedPnl());
        // 进场 2 手 1.00 + 出场 2 手 1.00：非反手时整笔都归这条记录，不切
        assertMoney("2.00", sealed.getCommission());
        assertMoney("10", sealed.netPnl());
        assertEquals(1, recordRepo.count(), "归零之后不开新记录");

        List<Trade> fills = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId);
        for (Trade fill : fills) {
            assertEquals(rec.getId(), fill.getTradeRecordId(), "两笔成交都归同一条记录");
        }
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
        // 三次成交的费都归这条记录：2 手 1.00 + 1 手 0.50 + 1 手 0.50
        assertMoney("2.00", rec.getCommission());
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
        // 进场 2 手 1.00 + 这笔 5 手里属于平仓腿的 2 手 1.00；那 5 手整笔收 2.50
        assertMoney("2.00", old.getCommission());
        assertMoney("10", old.netPnl(), "12 毛利 − 2.00 手续费");

        TradeRecord fresh = all.get(1);
        assertFalse(fresh.isClosed());
        assertEquals(2, fresh.getStartSeq(), "新记录与旧记录共享这笔成交的 seq");
        assertEquals(-1, fresh.getEndSeq());
        assertEquals(Side.SELL, fresh.getSide());
        assertMoney("3", fresh.getQuantity());
        assertMoney("4004.00", fresh.getEntryPrice());
        // 开仓腿 3 手 = 2.50 − 1.00，不是把整笔 2.50 再算一遍
        assertMoney("1.50", fresh.getCommission());
        assertNull(fresh.netPnl(), "还没收尾，净额是 null 而不是 0");

        List<Trade> trades = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId);
        assertEquals(old.getId(), trades.get(0).getTradeRecordId(), "开仓那笔归旧记录");
        assertEquals(old.getId(), trades.get(1).getTradeRecordId(),
                "反手这一笔归旧记录（和 realizedPnl 全额归旧记录同口径）");
    }

    // ---------------- 撤单 / 改价 / 退仓 ----------------

    @Test
    void cancelOpenOrderAndRejectCancellingAFilledOne() {
        trading.place(accountId, "buy", "limit", new BigDecimal("3998.00"), BigDecimal.ONE);
        String orderId = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId).get(0).getId();

        TradingService.Event event = trading.cancel(accountId, orderId);
        assertEquals("order_cancelled", event.action().wire());
        assertEquals(TradeOrder.Status.CANCELLED, orderRepo.findById(orderId).orElseThrow().getStatus());

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
     * 开一个手数上限 = {@code max} 的账户（档位只一档，利润是多少都落这一档）。
     *
     * <p>不用 {@code @BeforeEach} 那个 40 手的账户：40 手要真成交 40 手才到上限，
     * 用 1 手钉同一件事，成交笔数少好断言。
     */
    private String openAccountWithLimit(int max) {
        PropFirmTemplate capped = templates.create("限额 " + max, "测试", PropFirmStage.EVAL, null,
                new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, max))));
        return accounts.open(capped.getId(), SYMBOL, "限额账户").getId();
    }

    /**
     * 撞线：权益 ≤ 亏损线的那一根<strong>照常广播出去</strong>，账户当场判失败（终态），之后不再接单。
     *
     * <p>要点是「那根 K 线还得发」：人得看见是哪一根要了他的命。所以失败不是抛异常，
     * 而是把 {@code status} 放进同一条 {@code bar_advanced} 里。
     */
    @Test
    void stepFailsAccountWhenEquityTouchesLossLine() {
        // 40 手多单 @ 4001（EVAL 的唯一档就是 0→40 手）
        trading.market(accountId, "buy", new BigDecimal("40"), null);
        desk.step(accountId); // 09:05，低点 3999：浮亏 160，离 48000 还远
        desk.step(accountId); // 09:10

        // 下一交易日一根 3900 的插针：权益 49980 − 8080 = 41900，直接穿线（线在 48000）
        writeDay("2024-03-05", "2024-03-05 09:00,4000.00,4002.00,3900.00,3901.00,10");
        TradingService.Event event = desk.step(accountId);

        BarAdvancedPayload payload = (BarAdvancedPayload) event.payload();
        assertEquals("2024-03-05 09:00", payload.bar().time(), "撞线那一根照常发出去");
        assertEquals("FAILED", payload.pf().status());
        assertMoney("48000", payload.pf().lossLine());
        assertEquals(PropFirmAccount.Status.FAILED,
                accountRepo.findById(accountId).orElseThrow().getStatus(), "账户已判失败");
        assertThrows(BusinessException.class,
                () -> trading.market(accountId, "buy", BigDecimal.ONE, null), "失败后不再接单");
    }

    /**
     * 撞线那一刻<strong>自动收摊</strong>：市价平掉持仓、撤光挂单，平仓成交并进那一根。
     *
     * <p>为什么必须在判失败<strong>之前</strong>做完：失败之后账户就不能下单了，
     * 留一个仓位在账上就永远平不掉 —— 余额一直带着浮动盈亏漂，阶段流水里的结束余额也跟着失真。
     */
    @Test
    void stepClosesOutPositionAndOrdersWhenAccountFails() {
        trading.market(accountId, "buy", new BigDecimal("40"), null);          // 40 手多单 @ 4001
        trading.place(accountId, "sell", "limit", new BigDecimal("4300.00"), BigDecimal.ONE); // 远处挂单，不成交
        desk.step(accountId); // 09:05
        desk.step(accountId); // 09:10

        writeDay("2024-03-05",
                "2024-03-05 09:00,4000.00,4002.00,3900.00,3901.00,10",
                "2024-03-05 09:05,3901.00,3910.00,3895.00,3905.00,8");
        TradingService.Event event = desk.step(accountId);

        BarAdvancedPayload payload = (BarAdvancedPayload) event.payload();
        assertEquals("FAILED", payload.pf().status());
        assertEquals("flat", payload.position().side(), "撞线当场市价平仓，仓位不留到失败之后");
        assertEquals(0, payload.orders().stream().filter(o -> "open".equals(o.status())).count(),
                "撞线当场撤掉全部挂单");
        assertEquals(1, payload.fills().size(), "平仓那笔并进这一根，不另发 flat_all");
        assertMoney("3901.00", payload.fills().get(0).price(), "平在撞线那一根的收盘价");
        assertEquals("flat", trading.portfolio(accountId).position().side(), "库里也是平的");

        // 判死之后立刻再按【下一根】：这是用户真要做的动作（撞线那一根之后接着复盘）。
        // 失败是终态，这一根会重新判出 FAILED —— 但不能再"判死一次"，也不能抛。
        BarAdvancedPayload after = (BarAdvancedPayload) desk.step(accountId).payload();
        assertEquals("2024-03-05 09:05", after.bar().time(), "撞线之后还能继续往后推 K 线");
        assertEquals("FAILED", after.pf().status());
        assertEquals("flat", after.position().side());
        assertEquals(0, after.fills().size(), "空仓推盘面不再产生成交");
        assertEquals(PropFirmAccount.Status.FAILED,
                accountRepo.findById(accountId).orElseThrow().getStatus());
    }

    /**
     * 判失败之后账户<strong>仍然打得开、仍然能往后推 K 线</strong>，只有挂单 / 市价单被拒。
     *
     * <p>这是 2026-11-01 改的口径：「考挂了」不等于「这一场录像不许再看」。推盘面不改账、
     * 不改状态，只是「看到哪了」—— 而新单已经被 {@code requireTradable} 拦死了。
     *
     * <p>必须验<strong>连推多根</strong>而不是一根：失败是终态，每一根都会重新判出
     * {@code FAILED}，一旦哪一步忘了「账户已经不 OPEN 就别再判死一次」，第二根就会抛异常
     * —— 只推一根的用例发现不了这个。
     */
    @Test
    void failedAccountStillLoadsAndStepsButRejectsOrders() {
        trading.failAccount(accountId);

        com.tradingviewj.server.ws.DeskSnapshot snap = desk.snapshot(accountId);
        assertEquals("FAILED", snap.status(), "失败的账户也能拿到快照");
        assertFalse(snap.bars().isEmpty(), "K 线照常给");
        assertEquals("2024-03-04 09:00", snap.barTime(), "停在撞线/判死那一根");

        assertEquals("bar_advanced", desk.step(accountId).action().wire(), "失败之后还能往后翻一根（复盘）");
        assertEquals("bar_advanced", desk.step(accountId).action().wire(), "连推第二根也不许抛");

        PropFirmAccount after = accountRepo.findById(accountId).orElseThrow();
        assertEquals("2024-03-04 09:10", after.getLastBarTime(), "盘面时间真的往前走了两根");
        assertEquals(PropFirmAccount.Status.FAILED, after.getStatus(), "推盘面不会把账户「救活」");
        assertEquals(0, tradeRepo.count(), "空仓推盘面不产生任何成交");

        // 走到磁盘上最后一根，报的是「没有下一根」，不是「账户不能操作」
        BusinessException end = assertThrows(BusinessException.class, () -> desk.step(accountId));
        assertTrue(end.getMessage().contains("已经是磁盘上最后一根"), end.getMessage());

        BusinessException e = assertThrows(BusinessException.class,
                () -> trading.market(accountId, "buy", BigDecimal.ONE, null), "失败后不接市价单");
        assertTrue(e.getMessage().contains("已判失败"), e.getMessage());
        assertThrows(BusinessException.class,
                () -> trading.place(accountId, "buy", "limit", new BigDecimal("3998.00"), BigDecimal.ONE),
                "失败后不接挂单");
    }

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

    // ---------------- 日终强平：禁止交易时段 04:00–06:00 ----------------

    /**
     * 03:55 开仓 → 步进到 04:00：按 <b>04:00 的收盘价</b>市价平掉，挂单一起撤；
     * 04:00 那一根即便能扫到挂单，也<b>一笔都不许成交</b>（用户 2026-11-01 的规则）。
     *
     * <p>平仓价必须是 04:00 的 {@code close}（4006），不是 06:00 的开盘 4007，也不是 03:55 的 4011 ——
     * 04:00 是这一天最后一刻，账就停在那一刻的价上，缺口那段不该由用户承担。
     */
    @Test
    void sessionEndForceClosesAtTheFourOClockClose() {
        writeDay("2024-03-05",
                "2024-03-05 03:55,4010.00,4012.00,4009.00,4011.00,10",
                "2024-03-05 04:00,4011.00,4013.00,4005.00,4006.00,20",
                "2024-03-05 06:00,4007.00,4009.00,4006.00,4008.00,30",
                "2024-03-05 06:05,4008.00,4010.00,4007.00,4009.00,40");

        desk.step(accountId); // 09:05
        desk.step(accountId); // 09:10
        desk.step(accountId); // 03:55 —— 仍属于交易日 03-04，可交易
        trading.market(accountId, "buy", BigDecimal.ONE, null); // @4011
        // 一张在 04:00 一定会被扫到的买单：那一根 low = 4005，挂 4010 必触发
        trading.place(accountId, "buy", "limit", new BigDecimal("4010.00"), BigDecimal.ONE);
        assertEquals(1, tradeRepo.count(), "挂单不成交");

        desk.step(accountId); // 04:00 —— 走进禁交易时段

        List<Trade> trades = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId);
        assertEquals(2, trades.size(), "开仓 1 笔 + 日终强平 1 笔；那张挂单**不许**成交");
        Trade close = trades.get(1);
        assertEquals("2024-03-05 04:00", close.getBarTime(), "平仓记在 04:00 这一根上");
        assertMoney("4006.00", close.getPrice());
        assertMoney("-10", close.getRealizedPnl(), "(4006 − 4011) × 1 手 × 每点 2 美元");
        assertMoney("0.50", close.getCommission(), "手续费按每手每边收，是正数");
        assertMoney("-10.50", close.netPnl());

        assertEquals("flat", trading.portfolio(accountId).position().side(), "日终必须空仓");

        TradeOrder limit = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId).stream()
                .filter(o -> o.getType() == TradeOrder.Type.LIMIT)
                .findFirst().orElseThrow();
        assertEquals(TradeOrder.Status.CANCELLED, limit.getStatus(), "挂单在日终一起撤掉");
    }

    /**
     * 禁交易时段内下单：后端在 {@code requireTradable} 这一道闸上拒掉；06:05 之后恢复。
     *
     * <p>06:00 那根<b>归新的一天</b>（跨日结算发生在它身上），但它<b>同样不可交易</b> ——
     * 「归哪一天」和「能不能下单」是两件事。
     */
    @Test
    void ordersAreRejectedInsideTheClosedWindow() {
        writeDay("2024-03-05",
                "2024-03-05 03:55,4010.00,4012.00,4009.00,4011.00,10",
                "2024-03-05 04:00,4011.00,4013.00,4005.00,4006.00,20",
                "2024-03-05 06:00,4007.00,4009.00,4006.00,4008.00,30",
                "2024-03-05 06:05,4008.00,4010.00,4007.00,4009.00,40");
        desk.step(accountId);
        desk.step(accountId);
        desk.step(accountId); // 03:55
        desk.step(accountId); // 04:00

        BusinessException atFour = assertThrows(BusinessException.class,
                () -> trading.market(accountId, "buy", BigDecimal.ONE, null));
        assertTrue(atFour.getMessage().contains("禁止交易时段"), atFour.getMessage());

        desk.step(accountId); // 06:00 —— 也关着
        BusinessException atSix = assertThrows(BusinessException.class, () -> trading.place(
                accountId, "buy", "limit", new BigDecimal("4010.00"), BigDecimal.ONE));
        assertTrue(atSix.getMessage().contains("禁止交易时段"), atSix.getMessage());

        desk.step(accountId); // 06:05 —— 新交易日第一根可交易的 K 线
        assertEquals("order_placed", trading.place(accountId, "buy", "limit",
                new BigDecimal("4010.00"), BigDecimal.ONE).action().wire());
    }

    /**
     * 清掉上一条用例写下的日文件。
     *
     * <p>这些用例<strong>共用一个数据目录</strong>（整个类只有一个临时目录），
     * 而「磁盘上还有没有下一根」正是好几条用例的断言前提。少清一次，先跑过的用例
     * 写下的 {@code 2024-03-05.csv} 就会让后一条用例的「已经走到最后一根」变成假 ——
     * 而且失败信息会指向一个跟改动无关的地方。
     */
    private void clearBars() {
        if (!Files.exists(dataDir)) {
            return;
        }
        try (var walk = Files.walk(dataDir)) {
            walk.sorted(Comparator.reverseOrder())
                    .filter(p -> !p.equals(dataDir))
                    .forEach(p -> {
                        try {
                            Files.deleteIfExists(p);
                        } catch (IOException ignored) {
                            // 删不掉就留着：后面 writeDay 覆盖写，不影响断言
                        }
                    });
        } catch (IOException ignored) {
            // 同上
        }
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
