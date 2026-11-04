package com.tradingviewj.server.entity.trade;

import java.math.BigDecimal;

/**
 * 一批<strong>已经平掉</strong>的仓位：从哪一批开仓成交来、在哪一次平仓成交结束、
 * 平掉多少手、这一批自己赚了多少。<strong>值对象，不落库</strong>。
 *
 * <p>它和 {@link Trade} 的分工：Trade 是「一笔成交」（开仓/加仓/平仓/反手都算），
 * ClosedLot 是「一批仓位的完整生命周期」。<strong>一笔平仓成交可以同时平掉好几批</strong>，
 * 那就产出好几条 ClosedLot —— 画布上每根平仓虚线对应其中一条。
 *
 * <p>盈亏用<strong>这一批自己的开仓价</strong>算，不用平均成本。为什么不能拿平均成本法的结果
 * 按手数平摊：5 笔空单分别开在 7076.14 / 7081 / 7083.25 / 7081.5 / 7082.5，一起在 7079.25
 * 平掉，均价法算出总共 +40.70，平摊成 5 份就变成每条 +8.14 —— 7076.14 那笔明明亏了 3.11 点
 * 却显示盈利。按各自开仓价算才是真实的逐笔归因（-15.55 / +8.75 / +20 / +11.25 / +16.25），
 * 而且加起来正好等于均价法的 +40.70。
 *
 * <p>⚠️ 一笔平仓把<strong>全部</strong>持仓平掉时，各批盈亏之和与该笔 Trade 的 realizedPnl
 * 完全相等；只平掉一部分时两者会不同 —— 那是两种成本法的固有差异，不是算错。
 * <strong>仓位与账户口径一律以平均成本法为准</strong>，这里只负责逐批归因。
 */
public record ClosedLot(
        String openBarTime,
        BigDecimal openPrice,
        String closeBarTime,
        BigDecimal closePrice,
        Side side,
        BigDecimal quantity,
        BigDecimal realizedPnl
) {
}
