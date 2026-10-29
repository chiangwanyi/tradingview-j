package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * {@link TradeLedger} 的口径测试。
 *
 * <p>这三个断言钉住的是<strong>两套成本法的关系</strong>，也就是最容易写错的地方：
 * 均价法给账户看，FIFO 给画线看，两者在「全平」时必须相等、在「部分平」时必须不等。
 * 任何一次「顺手把两块合成一块」的重构都会在这里亮红灯。
 */
class TradeLedgerTest {

    /** MNQ：tick 0.25、tickValue 0.5 → 价格每走 1 点，一手值 2 美元。 */
    private static final BigDecimal MNQ_POINT_VALUE =
            TradeLedger.pointValue(new BigDecimal("0.25"), new BigDecimal("0.5"));

    @Test
    void pointValueFallsBackToOneWhenTickMissing() {
        assertEquals(BigDecimal.ONE, TradeLedger.pointValue(null, null));
        assertEquals(BigDecimal.ONE, TradeLedger.pointValue(BigDecimal.ZERO, new BigDecimal("0.5")));
        assertEquals(0, new BigDecimal("2").compareTo(MNQ_POINT_VALUE));
    }

    /**
     * 5 笔空单在同一个价位一起平掉。
     *
     * <p>期望：逐批盈亏各不相同的负数/正数混在一起，而且<strong>合计等于</strong>均价法那一笔。
     * 这条用例直接来自 Go 版踩过的坑 —— 当时前端把均价法的结果按手数平摊，
     * 5 条平仓线显示成 5 个一模一样的 +8.14，7076.14 那笔明明是亏的。
     */
    @Test
    void fifoLotsSumToAverageCostWhenFullyClosed() {
        List<Trade> trades = new ArrayList<>();
        trades.add(sell("2026-10-16 10:00", "7076.14", "1"));
        trades.add(sell("2026-10-16 10:05", "7081", "1"));
        trades.add(sell("2026-10-16 10:10", "7083.25", "1"));
        trades.add(sell("2026-10-16 10:15", "7081.5", "1"));
        trades.add(sell("2026-10-16 10:20", "7082.5", "1"));
        trades.add(buy("2026-10-16 10:25", "7079.25", "5"));

        TradeLedger.Ledger ledger = TradeLedger.replay(trades, MNQ_POINT_VALUE);

        assertTrue(ledger.position().isFlat(), "全部平掉后必须空仓");

        // 逐批：每一批用**自己的开仓价**算，五条各不相同
        assertEquals(5, ledger.closedLots().size());
        List<BigDecimal> pnls = ledger.closedLots().stream().map(ClosedLot::realizedPnl).toList();
        assertEquals(5, pnls.stream().distinct().count(), "五批盈亏必须各不相同，摊平就是算错");

        // (7079.25 - 7076.14) 是空单的亏损方向，所以这一批必须是负的
        assertTrue(pnls.get(0).signum() < 0, "7076.14 开的空单在 7079.25 平掉是亏损");

        // 各批之和 == 均价法的那一笔（全平时的恒等式）
        BigDecimal sumOfLots = pnls.stream().reduce(BigDecimal.ZERO, BigDecimal::add);
        assertEquals(0, sumOfLots.compareTo(ledger.realizedPnl()),
                "全平时逐批归因之和必须等于均价法的已实现盈亏");
    }

    /** 部分平仓：均价法已经实现了一部分，剩下的持仓均价不变。 */
    @Test
    void partialCloseKeepsAveragePrice() {
        List<Trade> trades = List.of(
                buy("2026-10-16 10:00", "100", "2"),
                sell("2026-10-16 10:05", "110", "1"));

        TradeLedger.Ledger ledger = TradeLedger.replay(trades, BigDecimal.ONE);

        assertEquals(0, new BigDecimal("1").compareTo(ledger.position().quantity()));
        assertEquals(0, new BigDecimal("100").compareTo(ledger.position().averagePrice()));
        // 1 手，赚 10 点，每点 1 美元
        assertEquals(0, new BigDecimal("10").compareTo(ledger.realizedPnl()));
        assertEquals(1, ledger.closedLots().size(), "只平掉一批的一部分，只产出一条归因");
    }

    /** 反手：多头被打成空头，均价必须重置成本笔成交价，而不是留在旧均价上。 */
    @Test
    void reverseResetsAveragePrice() {
        List<Trade> trades = List.of(
                buy("2026-10-16 10:00", "100", "1"),
                sell("2026-10-16 10:05", "90", "3"));

        TradeLedger.Ledger ledger = TradeLedger.replay(trades, BigDecimal.ONE);

        assertEquals(0, new BigDecimal("-2").compareTo(ledger.position().quantity()));
        assertEquals(0, new BigDecimal("90").compareTo(ledger.position().averagePrice()));
        assertEquals(0, new BigDecimal("-10").compareTo(ledger.realizedPnl()));
    }

    private static Trade sell(String barTime, String price, String qty) {
        return Trade.fill("s1", "MNQ", "o1", barTime, Side.SELL,
                new BigDecimal(price), new BigDecimal(qty), BigDecimal.ZERO, BigDecimal.ZERO);
    }

    private static Trade buy(String barTime, String price, String qty) {
        return Trade.fill("s1", "MNQ", "o1", barTime, Side.BUY,
                new BigDecimal(price), new BigDecimal(qty), BigDecimal.ZERO, BigDecimal.ZERO);
    }
}
