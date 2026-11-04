package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.dto.ClosedLotView;
import com.tradingviewj.server.dto.AnnotationView;
import com.tradingviewj.server.dto.AudioClipView;
import com.tradingviewj.server.dto.OrderView;
import com.tradingviewj.server.dto.PositionView;
import com.tradingviewj.server.dto.PfView;
import com.tradingviewj.server.dto.TradeView;
import com.tradingviewj.server.entity.audit.payload.VoiceState;

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
 * <p><strong>账也是快照的一部分</strong>（{@link #orders} 到 {@link #unrealizedPnl}）：
 * 断线重连后前端要按后端的样子重建整块盘面，而挂单、成交、持仓必须和 K 线一起重建 ——
 * 分开取就会出现「K 线是新的、挂单还是断线前那批」的错位。
 * 与 {@code bar_advanced} 广播里那几份是<strong>同一批视图类型</strong>，前端只有一套装载代码。
 *
 * <p>注意 {@code application.yml} 配了全局 {@code non_null}：取值为 null 的字段
 * （{@code tick} / {@code tickValue} 没配时）会整个消失，前端一律按 {@code undefined} 处理；
 * 但<strong>数组永远发</strong>（空的就发 {@code []}），因为它代表「确实是空的」。
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
        List<BarView> bars,
        /** 挂单全量（含已成交 / 已撤销），按挂单时间升序 */
        List<OrderView> orders,
        /** 成交流水，按（K 线时间, seq）升序 */
        List<TradeView> trades,
        /** 逐批平仓（画布上平仓虚线的数据源） */
        List<ClosedLotView> closedLots,
        PositionView position,
        /** <strong>净</strong>已实现盈亏（已扣手续费） */
        BigDecimal realizedPnl,
        BigDecimal unrealizedPnl,
        /**
         * 这块盘面上已经画好的全部图形。
         *
         * <p>和 K 线一起给，不分成第二条消息：重连之后「图上有哪些线」必须和「K 线揭示到哪一根」
         * 同时成立 —— 分开取会出现「K 线是新的、线还是断线前那批」的错位。
         * 按标的全取（图形不挂账户，换账户做同一张图，线还在）。
         */
        List<AnnotationView> annotations,
        /**
         * 考核读数：余额 / 日终峰值 / 亏损线 / 权益 / 是否已冻结。
         *
         * <p>和上面那几个盈亏数<strong>不是一回事</strong>：{@code realizedPnl} 是「这个账户
         * 一共赚了多少」（跨阶段、只增不减地累加），考核读数里的 {@code balance} 是
         * <strong>本阶段</strong>的余额（升级时被重置回账户规模）。两者在第一批成交之前相等，
         * 之后越离越远 —— 别拿一个去顶另一个。
         */
        PfView pf,
        /**
         * 当前这根 K 线上的语音讲解（「本根 x/9」和录音按钮的计数靠它）。
         *
         * <p>和 K 线一起给：横轴下方的录音条要一次摆对位置，晚一条消息到就会有
         * 「先画一条空带子、再长出标记」的闪动。
         */
        VoiceState voice,
        /** 整个标的的语音清单（录音条上「别处还有几条」、`‹n` / `n›` 的数据源）。 */
        List<AudioClipView> voiceAll
) {

    public static DeskSnapshot of(String accountId, String accountName, String stage, String status,
                                 String symbolId, String folder, BigDecimal tick, BigDecimal tickValue,
                                 String day, String barTime, boolean hasNext, List<BarView> bars,
                                 List<OrderView> orders, List<TradeView> trades,
                                 List<ClosedLotView> closedLots, PositionView position,
                                 BigDecimal realizedPnl, BigDecimal unrealizedPnl,
                                 List<AnnotationView> annotations, PfView pf,
                                 VoiceState voice, List<AudioClipView> voiceAll) {
        return new DeskSnapshot("snapshot", accountId, accountName, stage, status,
                symbolId, folder, tick, tickValue, day, barTime, hasNext, bars,
                orders, trades, closedLots, position, realizedPnl, unrealizedPnl, annotations, pf,
                voice, voiceAll);
    }
}
