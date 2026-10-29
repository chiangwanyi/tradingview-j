package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.exception.BusinessException;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

/**
 * 手数上限：<strong>每个标的的净持仓手数</strong>不能超过当前利润档位允许的数目。
 *
 * <h2>两条口径（用户 2026-10-29 确认）</h2>
 *
 * <ol>
 *   <li><strong>按净持仓算</strong>：买 40 手再卖 10 手，净持仓是 30 手，按 30 手判定。
 *       不做「多空两边各算各的」—— 那个口径下同时持 40 多和 40 空会显示成 80 手，
 *       而真实风险是 0。</li>
 *   <li><strong>每品种独立</strong>：MES 的 40 手和 MNQ 的 40 手互不占用。
 *       用户原话是「MGC / MES / MNQ 都适用同一档」，所以档位是全局的、额度是按品种的。</li>
 * </ol>
 *
 * <h2>档位由"当前阶段净盈利"决定，而且是实时的</h2>
 *
 * <p>不是开仓时锁定：余额从 999 涨到 1000 那一刻起，下一笔就能开 30 手。
 * 这符合真实平台的行为（手数上限跟着账户权益走），也避免了「上午被锁在 20 手，
 * 下午赚到 2000 还得重开账户」这种荒唐事。
 *
 * <p>这笔计算<strong>不属于实体</strong>：{@link PropFirmRules} 只存档位表，
 * 「取哪一档」是业务口径，按 AGENTS.md 放在 service。
 */
public final class PropFirmPositionLimit {

    private PropFirmPositionLimit() {
    }

    /**
     * 当前利润允许的最大净持仓手数。
     *
     * @param netProfit 当前阶段的净盈利（{@code balance − baseBalance}）；可以为负
     * @return 手数上限；{@code 0} 表示这套规则不限手数
     */
    public static int maxContracts(PropFirmRules rules, BigDecimal netProfit) {
        if (rules == null || rules.getPositionTiers() == null || rules.getPositionTiers().isEmpty()) {
            return 0;
        }
        // 档位按起点升序排一遍再找：库里存的是 JSON，谁写进去的顺序不能成为语义的一部分
        List<PositionTier> tiers = new ArrayList<>(rules.getPositionTiers());
        tiers.sort(Comparator.comparing(PositionTier::fromProfit));

        PositionTier chosen = null;
        for (PositionTier tier : tiers) {
            if (tier.covers(netProfit)) {
                chosen = tier;
            } else {
                break;
            }
        }
        // 利润低于第一档起点（例如一上来就亏了钱）：按第一档给，不给"无限手数"
        if (chosen == null) {
            chosen = tiers.get(0);
        }
        return chosen.maxContracts();
    }

    /**
     * 下单前的闸门：这一笔过后，该品种的净持仓会不会超限。
     *
     * <p>参数收的是<strong>成交之后的净持仓</strong>而不是"这一笔几手"：
     * 单笔校验挡不住「一手一手地加到 41 手」，而净持仓校验天然挡得住，还能防反手加仓
     * （多 39 手卖 40 手 → 净 −1，完全合法）。
     *
     * @param netProfit     当前阶段净盈利
     * @param symbolId      哪个品种（只用于报错信息）
     * @param netAfterFill  这一笔成交之后，该品种的净持仓（买正卖负的绝对值会被取）
     * @throws BusinessException 超限
     */
    public static void requireWithinLimit(PropFirmRules rules, BigDecimal netProfit,
                                          String symbolId, BigDecimal netAfterFill) {
        int limit = maxContracts(rules, netProfit);
        if (limit <= 0 || netAfterFill == null) {
            return;
        }
        BigDecimal held = netAfterFill.abs();
        if (held.compareTo(BigDecimal.valueOf(limit)) > 0) {
            throw new BusinessException("超出该阶段的手数上限：" + symbolId
                    + " 成交后净持仓 " + held.stripTrailingZeros().toPlainString()
                    + " 手，当前利润档位允许 " + limit + " 手");
        }
    }
}
