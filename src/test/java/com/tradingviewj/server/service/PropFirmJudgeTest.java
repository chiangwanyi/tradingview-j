package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.PfView;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmSettlement;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmSettlementRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRepository;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.config.AppProperties;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDate;
import java.util.List;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyList;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 两条「时钟」的口径：<strong>余额逐笔变、亏损线只在日终动</strong>。
 *
 * <p>这正是最容易实现错的地方（把两个混成一个，或让亏损线盘中上移），所以钉死它：
 *
 * <ul>
 *   <li>盘中成交之后，余额立刻变，<strong>亏损线不动</strong>；</li>
 *   <li>跨交易日那一步，日终余额进峰值，<strong>亏损线跟着上移</strong>；</li>
 *   <li>峰值越过冻结水位之后，亏损线停在 {@code trailingLockLevel − 最大回撤}。</li>
 * </ul>
 */
class PropFirmJudgeTest {

    private static final String FOLDER = "CME_MINI_MES1!";
    private static final String CODE = "CME_MINI:MES1!";

    @TempDir
    Path dataRoot;

    private PropFirmJudge judge;
    private TradeRepository trades;
    /** 这些用例的成交都当挂单成交看待（返回空表 = 没有市价单）。 */
    private OrderRepository orders;
    /** 日终判决的持久化层：默认返回空表（等于「还没结过账」），个别用例会喂历史记录。 */
    private PropFirmSettlementRepository settlements;

    @BeforeEach
    void setUp() throws Exception {
        AppProperties properties = new AppProperties();
        properties.setBarRoot(dataRoot.toString());
        CsvBarStore store = new CsvBarStore(properties);

        Path folder = Files.createDirectories(dataRoot.resolve(FOLDER));
        Files.writeString(folder.resolve("2024-03-04.csv"), """
                time,open,high,low,close,volume
                2024-03-04 09:00,4000.00,4001.00,3999.00,4000.00,100
                2024-03-04 09:05,4000.00,4006.00,4000.00,4006.00,100
                2024-03-04 09:10,4006.00,4007.00,4005.00,4006.00,100
                2024-03-04 09:15,4006.00,4006.00,4005.00,4005.00,100
                """);
        Files.writeString(folder.resolve("2024-03-05.csv"), """
                time,open,high,low,close,volume
                2024-03-05 09:00,4010.00,4011.00,4009.00,4010.00,100
                2024-03-05 09:05,4010.00,4010.00,4009.00,4009.00,100
                """);
        Files.writeString(folder.resolve("2024-03-06.csv"), """
                time,open,high,low,close,volume
                2024-03-06 09:00,4020.00,4021.00,4019.00,4020.00,100
                """);

        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("1.25"));
        SymbolRepository symbols = mock(SymbolRepository.class);
        when(symbols.findById(CODE)).thenReturn(Optional.of(symbol));

        // 每点价值 = 1.25 / 0.25 = 5 美元
        Trade open = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.BUY,
                new BigDecimal("4000.00"), new BigDecimal("2"), BigDecimal.ZERO, new BigDecimal("1.00"));
        Trade close = Trade.fill("acc", CODE, "o2", "2024-03-04 09:10", 2, Side.SELL,
                new BigDecimal("4006.00"), new BigDecimal("2"), new BigDecimal("60.00"), new BigDecimal("1.00"));
        trades = mock(TradeRepository.class);
        when(trades.findByAccountIdOrderByBarTimeAscSeqAsc(anyString())).thenReturn(List.of(open, close));

        orders = mock(OrderRepository.class);
        when(orders.findByAccountIdOrderByPlacedAtBarAsc(anyString())).thenReturn(List.of());

        // 默认「这一天都没结过账」：重放会现场算，结果进 pending。落库另有专门的测试。
        settlements = mock(PropFirmSettlementRepository.class);
        when(settlements.findByAccountIdAndStageOrderByTradingDayAsc(anyString(), any()))
                .thenReturn(List.of());

        judge = new PropFirmJudge(store, symbols, trades, orders, new BarCursorService(store), settlements);
    }

    @Test
    void balanceMovesPerFillDrawdownLineOnlyMovesAtDayEnd() {
        PropFirmAccount account = account();

        // 同一交易日内：余额 = 50000 − 1 + (60 − 1) = 50058，但日终还没结算 → 亏损线还停在 48000
        account.advanceBarTime("2024-03-04 09:10");
        PfView intraday = judge.settle(account, judge.assess(account));
        assertMoney("50058", intraday.balance());
        assertMoney("50000", intraday.eodPeakBalance());
        assertMoney("48000", intraday.lossLine());
        assertFalse(intraday.frozen(), "EVAL 目标 3000 还差得远，不该冻结");

        // 跨交易日：日终余额进峰值，亏损线跟着上移 58
        account.advanceBarTime("2024-03-05 09:00");
        PfView nextDay = judge.settle(account, judge.assess(account));
        assertMoney("50058", nextDay.balance());
        assertMoney("50058", nextDay.eodPeakBalance());
        assertMoney("48058", nextDay.lossLine());

        // 写回的是账户上那两个缓存列
        assertMoney("50058", account.getBalance());
        assertMoney("50058", account.getEodPeakBalance());
    }

    /**
     * 库里已经记过账的那一天，<strong>以库为准</strong>，不按这次重放重算。
     *
     * <p>这是「峰值只增不减」在跨请求层面唯一可靠的保证：结算点跟着 {@code lastBarTime} 走，
     * 只要允许重算，同一份成交停在不用的位置就会算出不同的峰值
     * （2026-11-03 机器人压测：61 条漂移，全在 03:55–05:55）。
     */
    @Test
    void aSettledDayInTheDatabaseWinsOverTheReplay() {
        PropFirmAccount account = account();
        account.advanceBarTime("2024-03-04 09:15");

        // 库里说这一天结在 50100（比这次重放算出来的 50058 高）
        when(settlements.findByAccountIdAndStageOrderByTradingDayAsc(anyString(), any()))
                .thenReturn(List.of(PropFirmSettlement.record("acc", PropFirmStage.EVAL, "2024-03-04",
                        new BigDecimal("50100.00"), new BigDecimal("50100.00"), BigDecimal.ZERO, 0,
                        PropFirmSettlement.Outcome.RUNNING, "还差 2900（目标 3000）")));

        PropFirmJudge.State state = judge.assess(account);
        assertMoney("50100", state.session().eodPeak());
        assertMoney("48100", state.session().floorLine());
        assertTrue(state.pending().isEmpty(), "这一天已经在库里，不该再产生待写记录");
    }

    /** 第一次走到日终：结算结果交给调用方落库，且**只交一次**（同一天不会被交两遍）。 */
    @Test
    void aFreshlySettledDayIsHandedToTheWriterExactlyOnce() {
        PropFirmAccount account = account();
        account.advanceBarTime("2024-03-04 09:15");

        PropFirmJudge.State state = judge.assess(account);
        assertEquals(1, state.pending().size(), "2024-03-04 走完了，应该正好一天等着落库");
        assertEquals(LocalDate.parse("2024-03-04"), state.pending().get(0).tradingDay());
        assertMoney("50058", state.pending().get(0).peakBalance());

        judge.settle(account, state);
        verify(settlements).saveAll(anyList());
    }

    /**
     * 达标（PROMOTED）只在<strong>日终</strong>判：两个盈利日各自 +1600，总量 3200 过 3000 的目标，
     * 一致性 50% 也没把目标抬上去（最大单日 1600 / 0.5 = 3200，正好相等）。
     * 这一条是「达标即冻结」的触发点 —— 冻结动作本身在 {@code TradeDeskService#step}。
     */
    @Test
    void promotionOnlyFiresAtDayEndAndFreezesTheAccount() {
        // 两天各赚 1600（净额），合计 3200 ≥ 目标 3000
        Trade day1 = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.SELL,
                new BigDecimal("4000.00"), BigDecimal.ONE, new BigDecimal("1601.00"), new BigDecimal("1.00"));
        Trade day2 = Trade.fill("acc", CODE, "o2", "2024-03-05 09:00", 2, Side.SELL,
                new BigDecimal("4010.00"), BigDecimal.ONE, new BigDecimal("1601.00"), new BigDecimal("1.00"));
        when(trades.findByAccountIdOrderByBarTimeAscSeqAsc(anyString())).thenReturn(List.of(day1, day2));

        PropFirmAccount account = account();

        // 第二天盘中：总量已经 3200 了，但还没跨日 → 不判达标
        account.advanceBarTime("2024-03-05 09:00");
        assertFalse(judge.assess(account).frozen(), "达标只在日终判，盘中不算");

        // 跨到第三天：第三天的第一根到达时，第二天才结算 → 达标
        account.advanceBarTime("2024-03-06 09:00");
        assertTrue(judge.assess(account).frozen(), "日终结算达标 → 冻结、等手动升级");
    }

    /**
     * 走到某交易日的<strong>最后一根</strong>，这一天就结算了 —— 不用等下一根来触发。
     *
     * <p>这是「EOD 追踪」的真实口径：结算发生在<strong>这一天收完</strong>那一刻，与「后面还有没有
     * 行情」无关。修复前的实现只在「新交易日的第一根」处结算，于是停在 09:15 这一天，
     * 辛苦赚来的 58 块缓冲当天进不了峰值，回撤线纹丝不动。
     */
    @Test
    void daySettlesAsSoonAsItHasNoLaterBarOfItsOwn() {
        PropFirmAccount account = account();

        account.advanceBarTime("2024-03-04 09:15");
        PfView atClose = judge.settle(account, judge.assess(account));
        assertMoney("50058", atClose.balance());
        assertMoney("50058", atClose.eodPeakBalance());
        assertMoney("48058", atClose.lossLine());
    }

    /**
     * 磁盘上<strong>再没有下一根</strong>时，最后一个交易日也要结算。
     *
     * <p>修复前这是最隐蔽的一条：跳空到最后一根之后，{@code onBar} 再也等不到「新一天的
     * 第一根」，峰值永远不更新、盈利日不计数、EVAL 达标也不冻结 —— 账户赚够了还能继续下单。
     */
    @Test
    void lastTradingDaySettlesWithoutAnyNextBar() {
        Trade only = Trade.fill("acc", CODE, "o9", "2024-03-06 09:00", 3, Side.SELL,
                new BigDecimal("4020.00"), BigDecimal.ONE, new BigDecimal("101.00"), new BigDecimal("1.00"));
        when(trades.findByAccountIdOrderByBarTimeAscSeqAsc(anyString())).thenReturn(List.of(only));

        PropFirmAccount account = account();

        account.advanceBarTime("2024-03-06 09:00");
        PfView last = judge.settle(account, judge.assess(account));
        assertMoney("50100", last.balance());
        assertMoney("50100", last.eodPeakBalance());
        assertMoney("48100", last.lossLine());
    }

    /**
     * 一致性上限会把通关目标<strong>抬高</strong>：单日赚 2750、比例 50% → 目标 5500，
     * 于是「总盈利 3250 已经过了模板的 3000」也不能升级。
     *
     * <p>钉三件事：
     *
     * <ul>
     *   <li>判定用的是抬高后的目标（3250 &lt; 5500 → 不冻结）；</li>
     *   <li>本阶段单日最大净盈利被写回账户缓存列（界面上的「目标为什么是 5500」靠它回答）；</li>
     *   <li>账户派生出来的 {@code effectiveProfitTarget} 与判定口径一致 —— 两处一旦分叉，
     *       界面就会显示一个自己达不到的数字。</li>
     * </ul>
     */
    @Test
    void consistencyRaisesTheTargetBeyondTheTemplateOne() {
        // 第一天净赚 2750（2751 − 1 手续费），第三天再净赚 500：合计 3250
        Trade day1 = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.SELL,
                new BigDecimal("4000.00"), BigDecimal.ONE, new BigDecimal("2751.00"), new BigDecimal("1.00"));
        Trade day3 = Trade.fill("acc", CODE, "o2", "2024-03-06 09:00", 2, Side.SELL,
                new BigDecimal("4020.00"), BigDecimal.ONE, new BigDecimal("501.00"), new BigDecimal("1.00"));
        when(trades.findByAccountIdOrderByBarTimeAscSeqAsc(anyString())).thenReturn(List.of(day1, day3));

        PropFirmAccount account = account();
        account.advanceBarTime("2024-03-06 09:00");
        PfView view = judge.settle(account, judge.assess(account));

        assertMoney("53250", view.balance());
        assertMoney("2750", account.getMaxDayNetBalance());
        assertMoney("5500", account.getEffectiveProfitTarget());
        assertFalse(view.frozen(), "3250 只过了模板的 3000，一致性把目标抬到 5500 了");
    }

    /**
     * 「哪些成交是按当根收盘价成交的市价单」<strong>必须返回成交 id</strong>，而不是订单 id。
     *
     * <p>这两组 id 是两批不同的 UUID。2026-11-03 这里给的是订单 id，而
     * {@link PropFirmSession} 拿 {@code trade.getId()} 去比 —— 集合永远命中不了，
     * 「市价单不承担同根极值」这条规则整条失效，而 {@code PropFirmSessionTest} 里传的是
     * trade id，所以单测一直是绿的。这条测试就是钉住这个接缝。
     */
    @Test
    void marketFillIdsAreTradeIdsNotOrderIds() {
        Trade market = Trade.fill("acc", CODE, "order-1", "2024-03-04 09:30", 1, Side.BUY,
                new BigDecimal("4000.00"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO);
        Trade another = Trade.fill("acc", CODE, "order-2", "2024-03-04 09:35", 2, Side.SELL,
                new BigDecimal("4010.00"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO);

        assertEquals(java.util.Set.of(market.getId()),
                PropFirmJudge.marketFillTradeIds(List.of(market, another), java.util.Set.of("order-1")),
                "按订单 id 匹配、返回成交 id");
        assertEquals(java.util.Set.of(),
                PropFirmJudge.marketFillTradeIds(List.of(market, another), java.util.Set.of()),
                "没有市价单订单时不该凭空命中");
        // 反向证据：如果实现又把订单 id 原样返回，这里会拿到 order-1 而不是 trade id
        assertFalse(PropFirmJudge.marketFillTradeIds(List.of(market), java.util.Set.of("order-1"))
                .contains("order-1"), "返回的是成交 id，不是订单 id");
    }

    /** EVAL 50K：规模 50000、目标 3000、回撤 2000、冻结水位 52100。 */
    private PropFirmAccount account() {
        PropFirmRules rules = new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                List.of(new PositionTier(BigDecimal.ZERO, 40)));
        PropFirmTemplate template = PropFirmTemplate.define("EVAL", null, PropFirmStage.EVAL, null, rules);
        return PropFirmAccount.open(template, CODE, "judge-test", "2024-03-04 09:00", "2024-03-04.csv");
    }

    private static void assertMoney(String expected, BigDecimal actual) {
        assertEquals(0, new BigDecimal(expected).compareTo(actual),
                "期望 " + expected + "，实际 " + actual);
    }
}
