package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.PfView;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
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
import java.util.List;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
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

        judge = new PropFirmJudge(store, symbols, trades, new BarCursorService(store));
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
