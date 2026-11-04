package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.PositionView;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.Money;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.Actor;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 记账算法的纯单元测试：不建库、不起 Spring、不写 CSV。
 *
 * <p>这里钉的是<strong>金额的精度</strong>，不是「大致对」—— 一条 {@code 834.99996} 的记录
 * 能在界面上被 {@code toFixed(2)} 盖成 835.00，只有落库的值和逐笔重放才看得见。
 * 所以断言按 {@code toPlainString()} 做<strong>字符串</strong>比较，连小数位数一起钉住。
 */
class TradeLedgerTest {

    /** MNQ 的每点价值：tickValue 0.5 / tick 0.25 = 2。 */
    private static final BigDecimal PV = new BigDecimal("2");

    /**
     * 六个价位的均价是循环小数（174586 / 6 = 29097.6666…），10 位有效数字截断后会算出 834.99996。
     *
     * <p>这是真实库里出现过的数（{@code trade_record.start_seq=3/end_seq=9}）：
     * 均价差 3e-6，被「6 手 × 点值 2」放大成 0.00004，于是 835 变成 834.99996。
     * 修法是均价用 DECIMAL128 + 金额出口收敛到分，这条测试同时钉住两者。
     */
    @Test
    void repeatingAveragePriceStillBooksExactCents() {
        List<Trade> trades = new ArrayList<>();
        String[] prices = {"29112.75", "29119.75", "29091.50", "29107.00", "29064.25", "29090.75"};
        for (int i = 0; i < prices.length; i++) {
            trades.add(fill(i + 1, "2026-07-20 09:" + (25 + i * 5), Side.BUY,
                    new BigDecimal(prices[i]), BigDecimal.ONE));
        }
        trades.add(fill(9, "2026-07-20 10:00", Side.SELL, new BigDecimal("29167.25"),
                new BigDecimal("6")));

        TradeLedger.Ledger ledger = TradeLedger.replay(trades, PV);

        assertEquals("835.00", ledger.realizedPnl().toPlainString(),
                "（29167.25 − 174586/6）× 6 手 × 2 点值 = 835 整，不许留尾数");
        assertTrue(ledger.position().isFlat(), "六手全平，不该还有持仓");

        // 逐批归因（FIFO）与平均成本法在「一次全平」时必然相等 —— 两者分叉就说明有一边算错了
        BigDecimal fifo = BigDecimal.ZERO;
        for (var lot : ledger.closedLots()) {
            fifo = fifo.add(lot.realizedPnl());
        }
        assertEquals("835.00", fifo.toPlainString(), "FIFO 逐批合计应与平均成本法一致");
    }

    /** 浮盈同样走均价，也必须收敛到分；否则「余额 = 起点 + 已实现 + 浮动」会带上无穷尾数。 */
    @Test
    void unrealizedPnlIsCappedAtCents() {
        Position position = TradeLedger.replay(List.of(
                fill(1, "2026-07-20 09:25", Side.BUY, new BigDecimal("29112.75"), BigDecimal.ONE),
                fill(2, "2026-07-20 09:35", Side.BUY, new BigDecimal("29119.75"), BigDecimal.ONE),
                fill(3, "2026-07-20 09:40", Side.BUY, new BigDecimal("29091.50"), BigDecimal.ONE),
                fill(4, "2026-07-20 09:45", Side.BUY, new BigDecimal("29107.00"), BigDecimal.ONE)
        ), PV).position();

        // 87324 + 29107 = 116431 / 4 = 29107.75，正好除得尽；除不尽的场景由上一条测试覆盖
        assertEquals("29107.75", position.averagePrice().toPlainString());

        BigDecimal pnl = position.unrealizedPnl(new BigDecimal("29167.25"), PV);
        assertEquals(2, pnl.scale(), "浮盈必须两位小数");
        assertEquals("476.00", pnl.toPlainString(), "(29167.25 − 29107.75) × 4 手 × 2 点值");
    }

    /** 金额归整本身：四舍五入、null 当 0、已经整的数不改变值。 */
    @Test
    void moneyRoundsHalfUpToCents() {
        assertEquals("834.99", Money.round(new BigDecimal("834.994")).toPlainString());
        assertEquals("835.00", Money.round(new BigDecimal("834.995")).toPlainString());
        assertEquals("835.00", Money.round(new BigDecimal("834.99996")).toPlainString());
        assertEquals("0", Money.round(null).toPlainString(), "null 当 0，免得每个调用点都判空");
        assertEquals("-103.00", Money.round(new BigDecimal("-103")).toPlainString());
    }

    /**
     * 落库前的闸门：<strong>调用方传什么进来，写进实体的都是两位小数</strong>。
     *
     * <p>这条测试的意义不在于现价（现价本来就干净），而在于以后新增一条记账路径时——
     * 只要它经过 {@code Trade.fill} / {@code TradeRecord.closeAt}，就不可能把
     * {@code 834.99996} 这种尾数带进库。库里一旦有这种数，余额、考核、导出全部跟着脏，
     * 而且事后分不清哪些是有意为之。
     */
    @Test
    void entityFactoriesForceCentScale() {
        Trade trade = Trade.fill("acc", "CME_MINI:MNQ1!", "order-1", "2026-07-20 10:00", 9,
                Side.SELL, new BigDecimal("29167.25"), new BigDecimal("6"),
                new BigDecimal("834.99996"), new BigDecimal("3.0000001"));
        assertEquals("835.00", trade.getRealizedPnl().toPlainString());
        assertEquals("3.00", trade.getCommission().toPlainString());
        assertEquals("832.00", trade.netPnl().toPlainString(), "净额也必须是两位小数");

        TradeRecord record = TradeRecord.open("acc", "1", "CME_MINI:MNQ1!",
                "2026-07-20 09:25", 3, Actor.HUMAN, "1");
        record.closeAt("2026-07-20 10:00", new BigDecimal("29167.25"),
                new BigDecimal("834.99996"), 9);
        assertEquals("835.00", record.getRealizedPnl().toPlainString());
    }

    /**
     * 均价是循环小数时，协议里只送两位 —— 34 位精度原样序列化会变成
     * {@code 29097.66666666666666666666666666667}，塞进每一帧 WS 和每条 audit payload。
     *
     * <p>但**只有展示收敛**：{@code Position} 本身仍是全精度，浮盈照旧用全精度算。
     */
    @Test
    void averagePriceIsShortenedForDisplayOnly() {
        BigDecimal repeating = new BigDecimal("174586").divide(new BigDecimal("6"),
                java.math.MathContext.DECIMAL128);
        Position position = new Position(new BigDecimal("6"), repeating);

        assertEquals("29097.67", PositionView.of(position).averagePrice().toPlainString());
        assertEquals(2, PositionView.of(position).averagePrice().scale());
        // 展示过的是 29097.67，算账还得用 29097.6666…，否则 (29167.25 − 29097.67)×12 = 834.96
        assertEquals("835.00", position.unrealizedPnl(new BigDecimal("29167.25"), PV).toPlainString());
    }

    private static Trade fill(int seq, String barTime, Side side, BigDecimal price, BigDecimal qty) {
        return Trade.fill("acc", "CME_MINI:MNQ1!", "order-" + seq, barTime, seq, side, price, qty,
                BigDecimal.ZERO, BigDecimal.ZERO);
    }
}
