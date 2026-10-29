package com.tradingviewj.server.entity.propfirm;

import java.math.BigDecimal;

/**
 * 手数上限的一档：账户净盈利达到 {@code fromProfit} 之后，每个标的的净持仓最多 {@code maxContracts} 手。
 *
 * <p><strong>为什么是"一档"而不是一个数字</strong>：LucidFlex 50K 的出金号按利润分档
 * （$0–999 给 20 手、$1,000–1,999 给 30 手、$2,000 起给 40 手），一条规则里放不下。
 * 分档表按 {@code fromProfit} 升序排列，取"最后一个 {@code fromProfit ≤ 当前利润}"那一档。
 *
 * <p><strong>它是净持仓手数</strong>：买 40 手再卖 10 手，净持仓是 30 手，按 30 手算。
 * 这条口径由 {@code service.PropFirmPositionLimit} 实现，本类只回答"利润到了哪一档"。
 *
 * <p>不叫"持仓上限"是因为它同时被用来限制"一次能下多少"：
 * 一道校验挡两件事，避免出现「单笔 40 手、连下两笔变成 80 手」的缝。
 */
public record PositionTier(BigDecimal fromProfit, int maxContracts) {

    public PositionTier {
        if (fromProfit == null || fromProfit.signum() < 0) {
            throw new IllegalArgumentException("分档起点必须是非负利润，收到: " + fromProfit);
        }
        if (maxContracts <= 0) {
            throw new IllegalArgumentException("手数上限必须是正整数，收到: " + maxContracts);
        }
    }

    /** 这一档是否覆盖给定的净盈利（含边界：{@code fromProfit} 是"达到即进入"）。 */
    public boolean covers(BigDecimal netProfit) {
        return netProfit != null && netProfit.compareTo(fromProfit) >= 0;
    }
}
