package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.trade.Trade;

import java.math.BigDecimal;

/**
 * 一笔成交<strong>在协议里</strong>的样子（前端 {@code Trade}）。
 *
 * <h2>三个金额字段，说的是三件事</h2>
 *
 * <ul>
 *   <li>{@link #realizedPnl} —— <strong>毛利</strong>：这一笔平掉的部分按平均成本法赚了多少，
 *       没扣手续费。开仓/加仓是 0。</li>
 *   <li>{@link #commission} —— 这一笔的<strong>单边</strong>手续费快照（每手每边 × 手数）。
 *       开仓也收，所以一笔往返在这里出现两次。</li>
 *   <li>{@link #netPnl} —— <strong>净额</strong> = 毛利 − 手续费。账户、考核、界面上的
 *       「已实现盈亏」全部用这个口径；开仓那一笔因此是<strong>负数</strong>（钱确实已经付了）。</li>
 * </ul>
 *
 * <p>三个都给，是为了让界面上「毛利 / 手续费 / 净额」这一行能各自显示，而不是让前端
 * 自己去做减法 —— 前端一算钱，就有了第二份口径。
 *
 * <p>时间字段叫 {@code time} 不叫 {@code barTime}：和 {@link BarView} 一致，
 * 画布按 {@code trade.time} 找横坐标。
 */
public record TradeView(
        String id,
        String orderId,
        String time,
        String side,
        BigDecimal price,
        BigDecimal quantity,
        BigDecimal realizedPnl,
        BigDecimal commission,
        BigDecimal netPnl
) {

    public static TradeView of(Trade trade) {
        return new TradeView(
                trade.getId(),
                trade.getOrderId(),
                trade.getBarTime(),
                trade.getSide().name().toLowerCase(java.util.Locale.ROOT),
                trade.getPrice(),
                trade.getQuantity(),
                trade.getRealizedPnl(),
                trade.getCommission(),
                trade.netPnl());
    }
}
