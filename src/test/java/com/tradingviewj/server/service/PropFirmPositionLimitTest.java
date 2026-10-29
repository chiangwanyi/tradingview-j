package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.exception.BusinessException;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

/**
 * 手数上限：<strong>每品种的净持仓</strong>，档位随"当前阶段净盈利"实时变化。
 *
 * <p>钉住三件容易写错的事：
 *
 * <ol>
 *   <li>档位边界是<strong>含</strong>的（赚到 1000 就进 30 手档，不是 1000.01）；</li>
 *   <li>判的是<strong>净持仓</strong>，不是"这一笔几手" —— 买 40 再卖 10 = 30 手合法；</li>
 *   <li>亏钱的时候按第一档给，<strong>不给</strong>"零手"或者"不限"。</li>
 * </ol>
 */
class PropFirmPositionLimitTest {

    private static final PropFirmRules FUNDED_TIERS = new PropFirmRules(
            new BigDecimal("50000"), null, new BigDecimal("2000"),
            PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100"),
            null, 5, new BigDecimal("150"),
            List.of(new PositionTier(BigDecimal.ZERO, 20),
                    new PositionTier(new BigDecimal("1000"), 30),
                    new PositionTier(new BigDecimal("2000"), 40)));

    @Test
    void tiersSwitchAtTheirBoundary() {
        assertEquals(20, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("0")));
        assertEquals(20, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("999.99")));
        assertEquals(30, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("1000")));
        assertEquals(30, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("1999.99")));
        assertEquals(40, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("2000")));
        assertEquals(40, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("99999")));
    }

    @Test
    void losingMoneyFallsBackToTheFirstTier() {
        assertEquals(20, PropFirmPositionLimit.maxContracts(FUNDED_TIERS, new BigDecimal("-1800")));
    }

    @Test
    void emptyTiersMeansNoLimit() {
        PropFirmRules unlimited = new PropFirmRules(
                new BigDecimal("50000"), new BigDecimal("3000"), new BigDecimal("2000"),
                PropFirmRules.DrawdownType.TRAILING_EOD, null, new BigDecimal("0.5"),
                null, null, List.of());
        assertEquals(0, PropFirmPositionLimit.maxContracts(unlimited, BigDecimal.TEN));
        assertDoesNotThrow(() -> PropFirmPositionLimit.requireWithinLimit(
                unlimited, BigDecimal.ZERO, "MES", new BigDecimal("500")));
    }

    @Test
    void nettingIsWhatCounts() {
        // 买 40 手之后再卖 10 手，净持仓 30 —— 20 手档下超了
        assertThrows(BusinessException.class, () -> PropFirmPositionLimit.requireWithinLimit(
                FUNDED_TIERS, BigDecimal.ZERO, "MES", new BigDecimal("30")));
        // 同样 30 手，但已经赚到 1000 → 30 手档，合法
        assertDoesNotThrow(() -> PropFirmPositionLimit.requireWithinLimit(
                FUNDED_TIERS, new BigDecimal("1000"), "MES", new BigDecimal("30")));
        // 空头按绝对值判：−21 手同样超限
        assertThrows(BusinessException.class, () -> PropFirmPositionLimit.requireWithinLimit(
                FUNDED_TIERS, BigDecimal.ZERO, "MES", new BigDecimal("-21")));
    }
}
