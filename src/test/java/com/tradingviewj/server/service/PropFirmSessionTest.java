package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.storage.Bar;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 考核判定的口径测试。这里每一条断言都对应一条<strong>用户明确给过</strong>的规则，
 * 所以改规则时应该先改这里，而不是先改实现。
 *
 * <p>统一用 MES：tick 0.25、tickValue 1.25 → <strong>价格每走 1 点，一手值 5 美元</strong>。
 * 于是「赚 1800」= 一手走 360 点，算起来不用找计算器。
 */
class PropFirmSessionTest {

    private static final String SYM = "CME_MINI:MES1!";

    // ------------------------------------------------------------ 口径 1：亏损线

    /**
     * 亏损线初值 = 50000 − 2000 = 48000；<strong>恰好等于</strong>也算失败。
     *
     * <p>而且判的是<strong>浮亏的极值</strong>：这根 K 线收在 4000（浮亏 0），
     * 但最低摸到 3600（浮亏 2000）—— 用收盘价会判"活着"，用最低价才判得出真相。
     * 这条就是用户说的「盘中持仓某根 K 线收盘后浮亏 400 就算失败」的实现。
     */
    @Test
    void breachingTheLineFailsEvenWhenTheBarClosesFlat() {
        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(),
                List.of(buy("2026-10-28 07:00", "4000", "1", "0")));

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        PropFirmSession.Step step = session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4010", "3600", "4000"));

        assertEquals(PropFirmSession.Event.FAILED, step.event());
        assertEquals(0, new BigDecimal("48000").compareTo(step.floor()));
        assertEquals(0, new BigDecimal("48000").compareTo(step.equity()), "权益恰好等于亏损线，算失败");
    }

    /** 同样一根 K 线，只要没到线下就还得继续 —— 失败不是"跌了就失败"。 */
    @Test
    void stayingAboveTheLineKeepsTheSessionRunning() {
        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(),
                List.of(buy("2026-10-28 07:00", "4000", "1", "0")));

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        PropFirmSession.Step step = session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4010", "3610", "4000"));

        assertEquals(PropFirmSession.Event.RUNNING, step.event());
        assertEquals(0, new BigDecimal("48050").compareTo(step.equity()));
    }

    // ------------------------------------------------------------ 口径 2：EOD 追踪 + 冻结

    /**
     * 日终余额创新高 → 亏损线跟着上移；到 52100 之后<strong>冻结在 50100</strong>。
     *
     * <p>注意收盘余额 52100 是在<strong>下一交易日第一根 K 线</strong>进来到达时才结算的
     * —— 这正是"日终"两个字的含义：盘中摸到 52100 不算。
     */
    @Test
    void trailingStopsRisingOnceBalanceReachesTheLockLevel() {
        List<Trade> trades = new ArrayList<>();
        // 第一天赚 2100（一手 420 点）→ 日终余额 52100
        trades.add(buy("2026-10-28 07:00", "4000", "1", "0"));
        trades.add(sell("2026-10-28 07:00", "4420", "1", "2100", "0"));
        // 第二天再赚 900
        trades.add(buy("2026-10-29 07:00", "4400", "1", "0"));
        trades.add(sell("2026-10-29 07:00", "4580", "1", "900", "0"));

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(), trades);

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4420", "4000", "4420"));
        // 盘中还没结算：线仍然是 48000
        assertEquals(0, new BigDecimal("48000").compareTo(session.floorLine()));

        // 下一交易日第一根 → 结算第一天
        PropFirmSession.Step settled = session.onBar(SYM, bar("2026-10-29 06:00", "4420", "4420", "4420", "4420"));
        assertEquals(PropFirmSession.Event.RUNNING, settled.event());
        assertEquals(0, new BigDecimal("52100").compareTo(session.eodPeak()), "日终余额进峰值");
        assertEquals(0, new BigDecimal("50100").compareTo(session.floorLine()), "52100 起线锁在 50100");

        session.onBar(SYM, bar("2026-10-29 07:00", "4400", "4580", "4400", "4580"));
        // 第三天第一根 → 结算第二天，余额 53000 但线已经冻结
        session.onBar(SYM, bar("2026-10-30 06:00", "4580", "4580", "4580", "4580"));
        assertEquals(0, new BigDecimal("53000").compareTo(session.eodPeak()));
        assertEquals(0, new BigDecimal("50100").compareTo(session.floorLine()), "冻结之后不再上移");
    }

    // ------------------------------------------------------------ 口径 3：一致性

    /**
     * 用户给的原例：目标 3000，某天赚了 1800 → 一致性 60% → 目标被抬到 3600。
     *
     * <p>所以「总盈利 3000」这一天<strong>不能</strong>升级；要一直做到 3600。
     * 这条是整条规则里最容易写错的地方：如果只比 {@code profitTarget}，
     * 这里会在 3000 就误判通过。
     */
    @Test
    void consistencyRaisesTheTargetAboveTheProfitTarget() {
        List<Trade> trades = new ArrayList<>();
        // 第 1 天 +1800（360 点）
        trades.add(buy("2026-10-28 07:00", "4000", "1", "0"));
        trades.add(sell("2026-10-28 07:00", "4360", "1", "1800", "0"));
        // 第 2 天 +1200（240 点）
        trades.add(buy("2026-10-29 07:00", "4360", "1", "0"));
        trades.add(sell("2026-10-29 07:00", "4600", "1", "1200", "0"));
        // 第 3 天 +600（120 点）
        trades.add(buy("2026-10-30 07:00", "4600", "1", "0"));
        trades.add(sell("2026-10-30 07:00", "4720", "1", "600", "0"));

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(), trades);

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4360", "4000", "4360"));

        // 结算第 1 天：单日最大 1800 → 目标 1800/50% = 3600
        PropFirmSession.Step s1 = session.onBar(SYM, bar("2026-10-29 06:00", "4360", "4360", "4360", "4360"));
        assertEquals(0, new BigDecimal("3600").compareTo(s1.effectiveTarget()));
        assertEquals(PropFirmSession.Event.RUNNING, s1.event(), "总盈利 1800 还差得远");

        session.onBar(SYM, bar("2026-10-29 07:00", "4360", "4600", "4360", "4600"));

        // 结算第 2 天：总盈利 3000 ≥ 模板目标 3000，但 < 一致性目标 3600 → 不许过
        PropFirmSession.Step s2 = session.onBar(SYM, bar("2026-10-30 06:00", "4600", "4600", "4600", "4600"));
        assertEquals(0, new BigDecimal("3000").compareTo(s2.balance().subtract(new BigDecimal("50000"))));
        assertEquals(0, new BigDecimal("3600").compareTo(s2.effectiveTarget()));
        assertEquals(PropFirmSession.Event.RUNNING, s2.event(), "到 3000 不能通关，一致性把它抬到 3600");

        session.onBar(SYM, bar("2026-10-30 07:00", "4600", "4720", "4600", "4720"));

        // 收工结算第 3 天：总盈利 3600 = 目标 → 升级
        PropFirmSession.Step done = session.finish();
        assertEquals(PropFirmSession.Event.PROMOTED, done.event());
        assertEquals(0, new BigDecimal("3600").compareTo(done.balance().subtract(new BigDecimal("50000"))));
    }

    // ------------------------------------------------------------ 口径 4：手续费

    /**
     * <strong>开仓就收一次手续费</strong>，不等平仓。
     *
     * <p>用户原话「开仓平仓都有手续费」。所以开仓那一根 K 线的余额就已经少了进场费 ——
     * 这笔钱真实发生过，不该拖到平仓才认。
     */
    @Test
    void commissionIsChargedOnEntryAndOnExit() {
        List<Trade> trades = List.of(
                buy("2026-10-28 07:00", "4000", "2", "1.00"),
                sell("2026-10-28 07:10", "4010", "2", "100", "1.00"));

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(), trades);

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        PropFirmSession.Step entry = session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4000", "4000", "4000"));
        assertEquals(0, new BigDecimal("49999").compareTo(entry.balance()), "2 手 × 0.5 = 1 美元进场费");

        PropFirmSession.Step exit = session.onBar(SYM, bar("2026-10-28 07:10", "4010", "4010", "4010", "4010"));
        // 毛利 100 − 出场费 1 = 99；余额 = 50000 − 1 + 99 = 50098
        assertEquals(0, new BigDecimal("50098").compareTo(exit.balance()));
        assertNull(exit.message());
    }

    // ------------------------------------------------------------ 口径 5：盈利日

    /** 一个交易日要算"盈利日"，净盈利必须<strong>达到</strong>门槛（150），差一分都不算。 */
    @Test
    void profitDayNeedsTheThresholdToBeReached() {
        List<Trade> trades = List.of(
                buy("2026-10-28 07:00", "4000", "1", "0"),
                sell("2026-10-28 07:00", "4029.8", "1", "149", "0")); // 29.8 点 × 5 = 149

        PropFirmSession session = new PropFirmSession(PropFirmStage.FUNDED, fundedRules(),
                new BigDecimal("50000"), symbols(), trades);

        session.onBar(SYM, bar("2026-10-28 06:00", "4000", "4000", "4000", "4000"));
        session.onBar(SYM, bar("2026-10-28 07:00", "4000", "4030", "4000", "4030"));
        session.onBar(SYM, bar("2026-10-29 06:00", "4030", "4030", "4030", "4030"));

        assertEquals(0, session.profitDays(), "149 不到 150，不算盈利日");
    }

    /** FUNDED：累计 5 个盈利日就升 PAYOUT，中间不需要连续，也不看总盈利。 */
    @Test
    void fiveProfitDaysPromoteToPayout() {
        List<Trade> trades = new ArrayList<>();
        String[] days = {"2026-10-28", "2026-10-29", "2026-10-30", "2026-10-31", "2026-11-01"};
        String[] opens = {"4000", "4040", "4080", "4120", "4160"};
        String[] closes = {"4040", "4080", "4120", "4160", "4200"};
        for (int i = 0; i < days.length; i++) {
            // 每天一手赚 40 点 = 200 美元
            trades.add(buy(days[i] + " 07:00", opens[i], "1", "0"));
            trades.add(sell(days[i] + " 07:00", closes[i], "1", "200", "0"));
        }

        PropFirmSession session = new PropFirmSession(PropFirmStage.FUNDED, fundedRules(),
                new BigDecimal("50000"), symbols(), trades);

        for (int i = 0; i < days.length; i++) {
            session.onBar(SYM, bar(days[i] + " 06:00", opens[i], opens[i], opens[i], opens[i]));
            session.onBar(SYM, bar(days[i] + " 07:00", opens[i], closes[i], opens[i], closes[i]));
        }
        // 第 4 天结算完只数到 4 个
        assertEquals(4, session.profitDays());

        // 第 6 天第一根 → 结算第 5 天 → 满 5 个
        PropFirmSession.Step step = session.onBar(SYM,
                bar("2026-11-02 06:00", "4200", "4200", "4200", "4200"));
        assertEquals(PropFirmSession.Event.PROMOTED, step.event());
        assertEquals(5, session.profitDays());
    }

    // ------------------------------------------------------------ 口径 6：禁交易窗口

    /** 04:45–06:00 的 K 线推进不了盘面 —— 它不属于任何交易日。 */
    @Test
    void barsInsideTheGapAreSkipped() {
        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, evalRules(),
                new BigDecimal("50000"), symbols(), List.of());

        PropFirmSession.Step step = session.onBar(SYM, bar("2026-10-28 05:00", "4000", "4000", "4000", "4000"));
        assertEquals(PropFirmSession.Event.SKIPPED, step.event());
        assertNull(step.tradingDay());
        assertTrue(step.message().contains("禁交易窗口"));
    }

    // ------------------------------------------------------------ 工具

    private static PropFirmRules evalRules() {
        return new PropFirmRules(new BigDecimal("50000"), new BigDecimal("3000"),
                new BigDecimal("2000"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100"), new BigDecimal("0.50"), null, null,
                List.of(new PositionTier(BigDecimal.ZERO, 40)));
    }

    private static PropFirmRules fundedRules() {
        return new PropFirmRules(new BigDecimal("50000"), null, new BigDecimal("2000"),
                PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100"),
                null, 5, new BigDecimal("150"),
                List.of(new PositionTier(BigDecimal.ZERO, 20),
                        new PositionTier(new BigDecimal("1000"), 30),
                        new PositionTier(new BigDecimal("2000"), 40)));
    }

    private static Map<String, Symbol> symbols() {
        Symbol mes = Symbol.of(SYM, "CME_MINI_MES1!", new BigDecimal("0.50"));
        mes.setTick(new BigDecimal("0.25"));
        mes.setTickValue(new BigDecimal("1.25"));
        return Map.of(SYM, mes);
    }

    private static Bar bar(String time, String open, String high, String low, String close) {
        return new Bar(time, new BigDecimal(open), new BigDecimal(high),
                new BigDecimal(low), new BigDecimal(close), BigDecimal.ZERO);
    }

    private static Trade buy(String barTime, String price, String qty, String commission) {
        return Trade.fill("A", SYM, "O", barTime, Side.BUY, new BigDecimal(price),
                new BigDecimal(qty), BigDecimal.ZERO, new BigDecimal(commission));
    }

    private static Trade sell(String barTime, String price, String qty, String realized, String commission) {
        return Trade.fill("A", SYM, "O", barTime, Side.SELL, new BigDecimal(price),
                new BigDecimal(qty), new BigDecimal(realized), new BigDecimal(commission));
    }
}
