package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.storage.Bar;

/**
 * 回放末尾往后延伸了一根<strong>真实成交过</strong>的 K 线（{@link ReplayAction#BAR_EXTENDED}）。
 * 对应 Go 的 {@code BarExtendedPayload}。
 *
 * <p>它和 {@link BarAdvancedPayload} 刻意分开，而不是复用后者：{@code BAR_ADVANCED} 带着
 * 「这一刻的订单 / 成交 / 持仓 / 盈亏」全量快照，那是实时撮合算出来的；而延伸只发生在这笔
 * 交易结束<strong>之后</strong>，那段时间没有任何成交。硬凑一份持仓快照，等于在回放里
 * 把仓位凭空改写一遍 —— 所以这里<strong>只有 K 线和游标</strong>，交易状态一个字段都不带。
 *
 * <p>{@code hasMore} 是「数据目录里还有没有更晚的日文件」：前端据此决定【加载下一根】
 * 还能点，还是显示「已经到数据末尾」。
 */
public record BarExtendedPayload(
        Bar bar,
        int cursor,
        String time,
        boolean hasMore
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.BAR_EXTENDED;
    }
}
