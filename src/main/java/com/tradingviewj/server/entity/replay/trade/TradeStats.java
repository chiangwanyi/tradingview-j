package com.tradingviewj.server.entity.replay.trade;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * **一笔交易**的统计（对应 Go 的 {@code replay.TradeStats}），由录制到的操作算出来。
 *
 * <p>口径（全部由后端算，前端只排版）：entry 取这一笔第一次成交；exit 取最后一次成交
 * （部分平仓时一路更新）；mfe/mae 是最大有利/不利波动（美元，mfe &gt;= 0 &gt;= mae）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class TradeStats {

    /** 开仓方向 */
    private TradeSide side;

    /** 开仓手数 */
    private double quantity;

    /** 持仓期间的最大手数（加减仓时与 quantity 不同） */
    private double maxQuantity;

    private String entryTime;

    private double entryPrice;

    private String exitTime;

    private double exitPrice;

    /** 这笔交易全部成交的已实现盈亏之和（美元） */
    private double realizedPnl;

    private double mfe;

    private double mae;

    /** 持仓期间完整走过的 K 线根数 */
    private int barsHeld;

    /** 成交笔数（开仓 + 加减仓 + 平仓） */
    private int fills;

    /** 这段记录里的操作步数（含起点） */
    private int steps;

    /** false = 记录结束时仓位还在（实例被删、程序退出、进程崩溃留下的草稿） */
    private boolean closed;
}
