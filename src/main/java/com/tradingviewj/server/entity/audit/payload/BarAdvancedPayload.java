package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.ReplayAction;

import java.math.BigDecimal;
import java.util.List;

/**
 * 推进了一根 K 线（{@link ReplayAction#BAR_ADVANCED}）。对应 Go 的 {@code BarAdvancedPayload}。
 *
 * <p>它一份负载同时是两件事：「多了一根 K 线」和「这一刻账上是什么样」。盘面状态
 * <strong>每次发全量</strong>（订单 / 成交 / 逐批平仓 / 持仓 / 盈亏），因为一次复盘通常只有
 * 个位数的单子 —— 全量发的代价很小，而让前端维护增量合并的逻辑极易出错。
 *
 * <p>{@code fills} 只装<strong>这一根</strong>触发的成交（多数时候为空），是画布上「刚成交」
 * 那一下闪烁的数据源；{@code trades} 才是历史全量，两者别混。
 *
 * <p>⚠️ {@link BarView} 是<strong>画布协议</strong>里的那根 K 线：时间字段叫 {@code time}
 * （CSV 原文叫 {@code barTime}），也没有 {@code ema20} —— 均线是展示派生值，前端现算，
 * 不进任何账目。这里刻意<strong>不发</strong> {@code storage.Bar}：那样同一条广播里的
 * {@code bar} 和快照里的 {@code bars[]} 会是两种字段名，前端得写两套映射。
 */
public record BarAdvancedPayload(
        BarView bar,
        int cursor,
        int hiddenBars,
        boolean hasMore,
        List<Order> orders,
        List<Trade> trades,
        List<ClosedLot> closedLots,
        Position position,
        BigDecimal realizedPnl,
        BigDecimal unrealizedPnl,
        List<Trade> fills,
        VoiceState voice
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.BAR_ADVANCED;
    }
}
