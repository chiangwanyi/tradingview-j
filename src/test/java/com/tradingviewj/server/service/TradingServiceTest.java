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
