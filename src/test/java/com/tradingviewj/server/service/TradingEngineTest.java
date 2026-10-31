package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.trade.TradeOrder;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.storage.Bar;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 撮合规则的纯单元测试：不建库、不起 Spring、不写 CSV。
 *
 * <p>这几条是<strong>钱算得对不对</strong>的第一道闸门（跳空修正、挂反的单），
 * 所以逐条钉住，而不是只在「跑一遍交易流程」里间接覆盖 —— 流程测试发现不了
 * 「方向写反」：那只会在跳空那一类数据上暴露。
 */
class TradingEngineTest {

    private static final BigDecimal TICK = new BigDecimal("0.25");

    private static Bar bar(String time, String open, String high, String low, String close) {
        return new Bar(time, new BigDecimal(open), new BigDecimal(high),
                new BigDecimal(low), new BigDecimal(close), BigDecimal.ZERO);
    }

    private static TradeOrder order(Side side, TradeOrder.Type type, String price) {
        return TradeOrder.place("acc", "CME_MINI:MES1!", side, type,
                new BigDecimal(price), BigDecimal.ONE, "2024-03-04 09:00");
    }

    // ---------------- 触发判定 ----------------

    /** 四种组合正好是两对镜像：限价等价格过来，止损在价格跑掉之后追。 */
    @Test
    void triggeredCoversFourCombinations() {
        Bar b = bar("2024-03-04 09:05", "4001", "4005", "3999", "4002");

        // 限价买：low ≤ 价
        assertTrue(TradingEngine.triggered(order(Side.BUY, TradeOrder.Type.LIMIT, "4000"), b));
        assertFalse(TradingEngine.triggered(order(Side.BUY, TradeOrder.Type.LIMIT, "3998"), b));
        // 限价卖：high ≥ 价
        assertTrue(TradingEngine.triggered(order(Side.SELL, TradeOrder.Type.LIMIT, "4004"), b));
        assertFalse(TradingEngine.triggered(order(Side.SELL, TradeOrder.Type.LIMIT, "4006"), b));
        // 止损买：high ≥ 价
        assertTrue(TradingEngine.triggered(order(Side.BUY, TradeOrder.Type.STOP, "4005"), b));
        assertFalse(TradingEngine.triggered(order(Side.BUY, TradeOrder.Type.STOP, "4006"), b));
        // 止损卖：low ≤ 价
        assertTrue(TradingEngine.triggered(order(Side.SELL, TradeOrder.Type.STOP, "3999"), b));
        assertFalse(TradingEngine.triggered(order(Side.SELL, TradeOrder.Type.STOP, "3998"), b));
    }

    /** 市价单不会以 OPEN 状态留在挂单里；真出现（数据坏了）也不该被当成触发。 */
    @Test
    void marketOrderNeverTriggersLater() {
        Bar b = bar("2024-03-04 09:05", "4001", "4005", "3999", "4002");
        assertFalse(TradingEngine.triggered(order(Side.BUY, TradeOrder.Type.MARKET, "4001"), b));
    }

    // ---------------- 成交价（跳空修正） ----------------

    /**
     * 开盘就跳过挂单价时，成交价是<strong>开盘价</strong>，不是挂单价。
     *
     * <p>方向写反的例子：跳空高开 106 的止损买单还按 102.5 成交 —— 那是白送 3.5 个点，
     * 每次跳空都送，回测数字会系统性偏乐观。
     */
    @Test
    void fillPriceTakesTheOpenWhenTheBarGapsPast() {
        Bar gap = bar("2024-03-04 09:05", "4010", "4012", "4008", "4011");

        // 限价买挂在 4000，开盘 4010（更低才成交在开盘）—— 这里开盘更高，按 min(挂单价, 开盘) = 4000
        assertEquals(new BigDecimal("4000.00"),
                TradingEngine.fillPrice(order(Side.BUY, TradeOrder.Type.LIMIT, "4000"), gap, TICK));
        // 止损买挂在 4005，开盘 4010 直接跳过 → 按开盘成交（拿不到更好的价）
        assertEquals(new BigDecimal("4010.00"),
                TradingEngine.fillPrice(order(Side.BUY, TradeOrder.Type.STOP, "4005"), gap, TICK));
        // 限价卖挂在 4015，开盘 4010 → min 取不到，max(挂单价, 开盘) = 4015
        assertEquals(new BigDecimal("4015.00"),
                TradingEngine.fillPrice(order(Side.SELL, TradeOrder.Type.LIMIT, "4015"), gap, TICK));
        // 止损卖挂在 4012，开盘 4010 就已经穿过 → 按开盘成交（不会再往好里走）
        assertEquals(new BigDecimal("4010.00"),
                TradingEngine.fillPrice(order(Side.SELL, TradeOrder.Type.STOP, "4012"), gap, TICK));
    }

    /** 没跳空时成交价就是挂单价。 */
    @Test
    void fillPriceIsTheLimitPriceWithoutGap() {
        Bar b = bar("2024-03-04 09:05", "4001", "4005", "3999", "4002");
        assertEquals(new BigDecimal("4000.00"),
                TradingEngine.fillPrice(order(Side.BUY, TradeOrder.Type.LIMIT, "4000"), b, TICK));
        assertEquals(new BigDecimal("4004.00"),
                TradingEngine.fillPrice(order(Side.SELL, TradeOrder.Type.LIMIT, "4004"), b, TICK));
    }

    // ---------------- 价格 / 手数校验 ----------------

    @Test
    void roundPriceSnapsToTick() {
        assertEquals(new BigDecimal("4000.00"), TradingEngine.roundPrice(new BigDecimal("4000.12"), TICK));
        assertEquals(new BigDecimal("4000.25"), TradingEngine.roundPrice(new BigDecimal("4000.30"), TICK));
        assertEquals(new BigDecimal("4000.25"), TradingEngine.roundPrice(new BigDecimal("4000.13"), TICK));
        // 没配 tick：两位小数
        assertEquals(new BigDecimal("4000.13"), TradingEngine.roundPrice(new BigDecimal("4000.129"), null));
    }

    @Test
    void quantityMustBeAPositiveWholeNumber() {
        assertEquals(3, TradingEngine.normalizeQuantity(new BigDecimal("3")));
        assertEquals(1, TradingEngine.normalizeQuantity(new BigDecimal("1.000")));

        assertThrows(BusinessException.class, () -> TradingEngine.normalizeQuantity(null));
        assertThrows(BusinessException.class, () -> TradingEngine.normalizeQuantity(BigDecimal.ZERO));
        assertThrows(BusinessException.class, () -> TradingEngine.normalizeQuantity(new BigDecimal("-2")));
        assertThrows(BusinessException.class, () -> TradingEngine.normalizeQuantity(new BigDecimal("0.5")));
        assertThrows(BusinessException.class,
                () -> TradingEngine.normalizeQuantity(BigDecimal.valueOf(TradingEngine.MAX_QUANTITY + 1L)));
    }

    @Test
    void sideAndPlacementTypeParseFromWire() {
        assertEquals(Side.BUY, TradingEngine.parseSide("buy"));
        assertEquals(Side.SELL, TradingEngine.parseSide("SELL"));
        assertEquals(TradeOrder.Type.LIMIT, TradingEngine.parsePlacementType("limit"));
        assertEquals(TradeOrder.Type.STOP, TradingEngine.parsePlacementType("stop"));

        assertThrows(BusinessException.class, () -> TradingEngine.parseSide("long"));
        assertThrows(BusinessException.class, () -> TradingEngine.parseSide(null));
        // 市价单走 market_order 命令：这里收下它只会让「下单即成交」多出一条路径
        BusinessException e = assertThrows(BusinessException.class,
                () -> TradingEngine.parsePlacementType("market"));
        assertTrue(e.getMessage().contains("market_order"));
        assertThrows(BusinessException.class, () -> TradingEngine.parsePlacementType("stop_limit"));
    }

    @Test
    void eligibleSkipsOrdersPlacedOnTheCurrentBar() {
        TradeOrder early = TradeOrder.place("acc", "CME_MINI:MES1!", Side.BUY, TradeOrder.Type.LIMIT,
                new BigDecimal("4000"), BigDecimal.ONE, "2024-03-04 09:00");
        TradeOrder sameBar = TradeOrder.place("acc", "CME_MINI:MES1!", Side.BUY, TradeOrder.Type.LIMIT,
                new BigDecimal("4000"), BigDecimal.ONE, "2024-03-04 09:05");

        List<TradeOrder> candidates = TradingEngine.eligible(List.of(early, sameBar), "2024-03-04 09:05");
        assertEquals(1, candidates.size());
        assertEquals(early.getId(), candidates.get(0).getId());
    }
}
