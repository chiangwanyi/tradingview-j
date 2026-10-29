package com.tradingviewj.server.entity.session;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * 指标<strong>定义</strong>，不是指标值。
 *
 * <p>为什么值不入库：指标完全由 K 线 + 参数决定，K 线在 CSV 里、参数在这张表里，
 * 那么值随时可以重算。存一份值进来就多了一份「和 CSV 不同步」的可能，而重算的代价
 * 是遍历一天几百根，可以忽略。
 *
 * <p>这条规则在 Go 版里已经用血换过一次：EMA20 曾经在 Go 里算好塞进每一根 Bar 下发，
 * ATR 则由前端拿着 OHLC 自己算 —— 结果是两边各有一套口径。现在「指标怎么算」
 * 只有一个答案（{@code service} 里的实现），存下来的只有「算什么」。
 */
@Getter
@Entity
@Table(name = "indicator")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Indicator extends BaseEntity {

    @Column(nullable = false)
    private String sessionId;

    @Setter
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Type type;

    @Setter
    @Column(nullable = false)
    private int period;

    /** 用哪个价格算。 */
    @Setter
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Source source;

    /** 其余参数（例如 ATR 的平滑方式）的 JSON。不做列 —— 参数集随指标类型变。 */
    @Setter
    private String paramsJson;

    @Setter
    @Column(nullable = false)
    private boolean visible = true;

    /** 图上叠放的顺序（小的画在下面）。 */
    @Setter
    @Column(nullable = false)
    private int sortOrder;

    public static Indicator of(String sessionId, Type type, int period, Source source) {
        Indicator indicator = new Indicator();
        indicator.sessionId = sessionId;
        indicator.type = type;
        indicator.period = period;
        indicator.source = source;
        return indicator;
    }

    public enum Type {
        /** 指数移动平均。 */
        EMA,
        /** 简单移动平均。 */
        SMA,
        /** Wilder 平滑（ATR 的经典口径）。 */
        RMA,
        /** 加权移动平均。 */
        WMA,
        /** 真实波幅均值。 */
        ATR
    }

    public enum Source {
        OPEN, HIGH, LOW, CLOSE,
        /** (最高 + 最低) / 2。 */
        HL2
    }
}
