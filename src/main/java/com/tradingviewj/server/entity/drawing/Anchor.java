package com.tradingviewj.server.entity.drawing;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;

/**
 * 图形上的一个锚点：横坐标是「某根 K 线的时间 + 向右延伸的根数」，纵坐标是价格。
 *
 * <p><strong>为什么横坐标不直接就是一个时间字符串</strong>：射线/矩形/斐波那契经常要画到
 * 「最后一根 K 线的右边」。如果用外推出来的未来时间当坐标，遇到周末休市就错位；
 * 而且等新 K 线揭示出来，同一个未来时间会落到另一个下标上，画好的线会自己左右跳。
 * 锚定在一根真实存在的 K 线上、再按「根数」偏移，屏幕位置就永远不动。
 *
 * <p>{@code barTime} 必须是已揭示数据里某一根的时间 —— 指向一根还没出现的 K 线，
 * 等于提前泄露后面的行情。
 */
@Getter
@Embeddable
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Anchor {

    @Column(name = "bar_time", nullable = false)
    private String barTime;

    @Column(nullable = false)
    private BigDecimal price;

    /** 从锚点那根 K 线往右再走多少根；0 = 就在锚点中心。 */
    @Column(nullable = false)
    private int offset;

    public Anchor(String barTime, BigDecimal price, int offset) {
        if (offset < 0) {
            throw new IllegalArgumentException("offset 不能为负: " + offset);
        }
        this.barTime = barTime;
        this.price = price;
        this.offset = offset;
    }
}
