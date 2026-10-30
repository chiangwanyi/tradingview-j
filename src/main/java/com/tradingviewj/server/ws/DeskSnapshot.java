package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarView;

import java.math.BigDecimal;
import java.util.List;

/**
 * 连上之后的第一条回执：<strong>这个账户的盘面现在是什么样</strong>。
 *
 * <p>为什么要有它，而不是让前端继续用 HTTP 拉数据再各自对齐：
 * 盘面现在停在哪一根、还有没有下一根、K 线窗口从哪里开始，<strong>这三件事只有后端知道</strong>
 * （它要读 CSV、算上一交易日、看账户的 {@code lastBarTime}）。前端拼一份、后端再拼一份，
 * 两份只要有一天不一致，用户看到的就是「按了【下一根】，图上跳了两格」。
 *
 * <p>{@link #bars} 和 HTTP {@code /api/bars} 返回的是同一段窗口（上一交易日整天 + 截止那天
 * 到 {@link #barTime} 那一根），字段也一致 —— 这样前端只有一条「装上盘面」的路径，
 * 不管是 WS 接上来的还是 HTTP 取回来的。
 *
 * <p>注意 {@code application.yml} 配了全局 {@code non_null}：取值为 null 的字段
 * （{@code tick} / {@code tickValue} 没配时）会整个消失，前端一律按 {@code undefined} 处理。
 */
public record DeskSnapshot(
        String type,
        String accountId,
        String accountName,
        /** EVAL / FUNDED / PAYOUT */
        String stage,
        /** ACTIVE / FAILED */
        String status,
        String symbolId,
        String folder,
        BigDecimal tick,
        BigDecimal tickValue,
        /** 截止那一天的日期（{@code yyyy-MM-dd}） */
        String day,
        /** 盘面停在哪一根（{@code yyyy-MM-dd HH:mm}） */
        String barTime,
        /** 磁盘上还有没有下一根。【下一根】按钮的可用性就看它 */
        boolean hasNext,
        List<BarView> bars
) {

    public static DeskSnapshot of(String accountId, String accountName, String stage, String status,
                                 String symbolId, String folder, BigDecimal tick, BigDecimal tickValue,
                                 String day, String barTime, boolean hasNext, List<BarView> bars) {
        return new DeskSnapshot("snapshot", accountId, accountName, stage, status,
                symbolId, folder, tick, tickValue, day, barTime, hasNext, bars);
    }
}
