package com.tradingviewj.server.dto;

import java.math.BigDecimal;
import java.util.List;

/**
 * 交易日历上的一天：这个账户在这个交易日的成交汇总。
 *
 * <p><strong>「日」是交易日，不是日历日</strong>：{@code 2026-10-29} 这一格装的是
 * {@code [10-29 06:00, 10-30 04:00)} 之间的成交 —— 半夜 03:10 平的那笔算在前一天。
 * 口径来自 {@code PropFirmClock.tradingDayOf}，和日终结算、判失败同一条，
 * 这里不另算一遍（两份口径迟早会在跨零点那几天分叉）。
 *
 * <p>金额一律是<strong>净额</strong>（已扣手续费）：日历格子里显示的是「这天赚了多少」，
 * 毛利减手续费的账后端已经算完（{@link TradeRecordView#netPnl()}），前端不再自己相减。
 *
 * <p>{@code items} 是这一天的每一笔（同 {@link TradeRecordView}），按开仓时间升序 ——
 * 点开格子时直接列出来，不必再发一次请求。只放<strong>已平仓</strong>的记录：
 * 还没结束的那笔归到哪一天都不对，盈亏也还在浮动。
 */
public record DailyPnlView(
        /** 交易日，`yyyy-MM-dd` */
        String day,
        /** 这一天的平仓笔数 */
        int trades,
        int wins,
        int losses,
        /** 这一天的净额（毛利 − 手续费），已收敛到分 */
        BigDecimal netPnl,
        BigDecimal grossPnl,
        BigDecimal commission,
        List<TradeRecordView> items
) {
}
