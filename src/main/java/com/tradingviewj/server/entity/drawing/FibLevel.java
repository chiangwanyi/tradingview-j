package com.tradingviewj.server.entity.drawing;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;

/**
 * 斐波那契回撤的一条水平线。
 *
 * <p>{@code ratio} 是「相对起点到终点这段价格区间」的倍数：0 = 起点价、1 = 终点价、
 * -1 = 起点再往外一个区间。四条线永远水平且互相平行 —— 用户只能改颜色，
 * 不能单独拖某一条（拖了一条，另外三条就不再是「同一个区间的比例」了）。
 */
@Getter
@Embeddable
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class FibLevel {

    @Column(name = "ratio", nullable = false)
    private BigDecimal ratio;

    @Column(name = "color")
    private String color;

    public FibLevel(BigDecimal ratio, String color) {
        this.ratio = ratio;
        this.color = color;
    }
}
