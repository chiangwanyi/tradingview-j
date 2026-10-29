package com.tradingviewj.server.entity.session;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * 复盘会话：一个标的 + 一个锚点 = 一块可以往前推进的盘面。
 *
 * <p>名字不叫 {@code Instance}：在 Spring 语境里 "instance" 默认指 bean 实例，
 * 读代码的人每次都要停顿一下。它做的事情是「回放一段历史行情」，叫会话更准。
 * <strong>注意这只是领域里的名字，WS 协议里的消息名和 {@code /api/instances/} 路径不动</strong>
 * —— 传输模型和领域模型是两回事，不需要名字一致。
 *
 * <p><strong>职责边界</strong>：它只管「这块盘面推进到哪了、谁在操作」。
 * 图形、挂单、成交、录音、指标都是<strong>独立实体</strong>，通过 {@code sessionId}
 * 引用它，不是它内部的集合。这样做的代价是取整块盘面要发若干个查询，
 * 换来的是：任何一张表都能单独分页、单独统计，不会为了拿一个游标就把几万条成交全捞出来。
 *
 * <p><strong>它不存的东西</strong>（这是与 Go 版最大的结构差异）：
 *
 * <ul>
 *   <li>{@code Tick} / {@code TickValue} → 从 {@link com.tradingviewj.server.entity.market.Symbol} 查。</li>
 *   <li>{@code Position} / {@code RealizedPnl} → 由成交流水现算（见 {@code service.TradeLedger}）。
 *       存一份副本就一定会有一天和成交对不上，而那种不一致没有任何办法自动修复。</li>
 *   <li>{@code Bars} → 在 CSV 里，一根都不进这个库。</li>
 * </ul>
 */
@Getter
@Entity
@Table(name = "replay_session")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class ReplaySession extends BaseEntity {

    /** 标的代码，外键指向 {@code symbol.id}。 */
    @Column(nullable = false)
    private String symbolId;

    /** 锚点 K 线的时间明文（{@code "2026-10-16 12:00"}），不是时间戳。 */
    @Column(nullable = false)
    private String anchorBarTime;

    /** 已经揭示到第几根（0 基）。这是回放进度，也是「未来行情不泄露」的闸门。 */
    @Setter
    @Column(nullable = false)
    private int cursorIndex;

    /** 当前接管盘面的人；NULL = 没人接管。 */
    @Setter
    private String operatorName;

    @Column(nullable = false, updatable = false)
    private String createdBy;

    public static ReplaySession open(String symbolId, String anchorBarTime, String createdBy) {
        ReplaySession session = new ReplaySession();
        session.symbolId = symbolId;
        session.anchorBarTime = anchorBarTime;
        session.createdBy = createdBy;
        session.cursorIndex = 0;
        return session;
    }
}
