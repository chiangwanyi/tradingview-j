package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;

/**
 * 成交流水号（{@code seq}）为什么是必需品，以及它到底改变了什么。
 *
 * <p>这个测试回答一个很容易被糊过去的疑问：「同一根 K 线上的几笔成交，顺序真有关系吗？
 * 反正最后持仓和总盈亏都一样。」
 *
 * <p><strong>总盈亏确实一样</strong>——那是现金流恒等式：全部平完时
 * {@code Σ(卖出价×量) − Σ(买入价×量)} 与先后无关。所以只盯着余额看，永远发现不了问题。
 *
 * <p>但<strong>逐批归因不一样</strong>：平仓虚线上每一条显示的是「这一批仓位自己赚了多少」，
 * 它取决于哪一笔先被认成开仓。两根 K 线之间看不出差别，可一旦要回答
 * 「这笔交易里哪一手赚得最多」「进场那一下是什么价位」，顺序就必须是确定的。
 *
 * <p>更要紧的是<strong>确定性</strong>：按主键（UUID）排序的话，同一份数据每次读出来
 * 顺序都可能不同，同一笔复盘两次打开给出不同的逐批归因——这种 bug 没人能查。
 *
 * <p>所以规则是：写库时分配 {@code seq}，重放前用 {@link Trade#executionOrder()} 排序，
 * 结果与「从库里读出来的顺序」无关。
 */
class TradeLedgerConsistencyTest {

    /** 三笔成交全在同一根 K 线上 —— 这正是 barTime 排不出先后、必须靠 seq 的场合。 */
    private static final String BAR = "2026-10-16 12:00";

    /**
     * 不排序就重放，逐批归因会随输入顺序变；排完序就唯一。
     *
     * <p>这个算例故意让"开仓"和"平仓"的归属发生翻转：
     * 正序是「先买 2 手，再分两次卖掉」，倒序是「先卖 1 手开空，再买 2 手（平空 + 反手做多），再卖 1 手」。
     * 两种走法总盈亏都是 +30，但第一批的进场价一个是 100、一个是 120。
     */
    @Test
    void withoutSortingTheSameFillsAttributeDifferently() {
        Trade first = fill(1, Side.BUY, "100", "2");
        Trade second = fill(2, Side.SELL, "110", "1");
        Trade third = fill(3, Side.SELL, "120", "1");

        TradeLedger.Ledger inOrder = TradeLedger.replay(List.of(first, second, third), BigDecimal.ONE);
        TradeLedger.Ledger shuffled = TradeLedger.replay(List.of(third, first, second), BigDecimal.ONE);

        // 总额一致：这就是「只看余额发现不了问题」的原因
        assertEquals(0, new BigDecimal("30").compareTo(inOrder.realizedPnl()));
        assertEquals(0, new BigDecimal("30").compareTo(shuffled.realizedPnl()));

        // 逐批归因不一致：第一批的进场价和它自己赚了多少都变了
        ClosedLot expected = inOrder.closedLots().get(0);
        ClosedLot actual = shuffled.closedLots().get(0);
        assertEquals(0, new BigDecimal("100").compareTo(expected.openPrice()));
        assertEquals(0, new BigDecimal("10").compareTo(expected.realizedPnl()));
        assertEquals(0, new BigDecimal("120").compareTo(actual.openPrice()));
        assertNotEquals(expected.openPrice(), actual.openPrice(),
                "顺序变了，逐批归因就得跟着变 —— 所以它必须是确定的");
    }

    /** 排完序之后，结果与输入顺序无关；排序键是 seq，不是主键。 */
    @Test
    void sortingByExecutionOrderMakesTheResultIndependentOfInputOrder() {
        Trade first = fill(1, Side.BUY, "100", "2");
        Trade second = fill(2, Side.SELL, "110", "1");
        Trade third = fill(3, Side.SELL, "120", "1");

        List<Trade> messy = new ArrayList<>(List.of(third, first, second));
        messy.sort(Trade.executionOrder());

        assertEquals(List.of(1, 2, 3), messy.stream().map(Trade::getSeq).toList());

        TradeLedger.Ledger sorted = TradeLedger.replay(messy, BigDecimal.ONE);
        TradeLedger.Ledger canonical = TradeLedger.replay(List.of(first, second, third), BigDecimal.ONE);

        assertEquals(canonical.realizedPnl(), sorted.realizedPnl());
        assertEquals(canonical.closedLots(), sorted.closedLots());
        assertEquals(canonical.position().quantity(), sorted.position().quantity());
        assertEquals(canonical.position().averagePrice(), sorted.position().averagePrice());
    }

    /**
     * 每一笔成交的净额都是「毛利 − 这一笔的单边手续费」。
     *
     * <p>开仓那一笔的毛利是 0，所以净额是<strong>负数</strong>——这不是 bug：钱在开仓那一刻
     * 就已经付了。正因为如此，任何「只在平仓时累加净额」的增量写法都会把进场费丢掉，
     * 两条路从此差一个单边费率，且越往后差得越多。
     */
    @Test
    void openingFillCostsCommissionEvenWithZeroGrossPnl() {
        Trade open = Trade.fill("A", "MES", "o1", BAR, 1, Side.BUY,
                new BigDecimal("100"), new BigDecimal("1"), BigDecimal.ZERO, new BigDecimal("1.25"));
        Trade close = Trade.fill("A", "MES", "o2", BAR, 2, Side.SELL,
                new BigDecimal("110"), new BigDecimal("1"), new BigDecimal("10"), new BigDecimal("1.25"));

        assertEquals(0, new BigDecimal("-1.25").compareTo(open.netPnl()), "开仓只付进场费");
        assertEquals(0, new BigDecimal("8.75").compareTo(close.netPnl()), "平仓赚 10、再付一次费");

        // 一笔往返的净额 = 毛利 − 两次单边费（这里每点价值按 1 算）
        BigDecimal roundTrip = open.netPnl().add(close.netPnl());
        assertEquals(0, new BigDecimal("7.50").compareTo(roundTrip));
    }

    private static Trade fill(int seq, Side side, String price, String qty) {
        return Trade.fill("A", "MES", "o" + seq, BAR, seq, side,
                new BigDecimal(price), new BigDecimal(qty), BigDecimal.ZERO, BigDecimal.ZERO);
    }
}
