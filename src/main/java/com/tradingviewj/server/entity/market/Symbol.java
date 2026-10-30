package com.tradingviewj.server.entity.market;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;

/**
 * 标的（合约品种）。
 */
@Getter
@Entity
@Table(name = "symbol")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Symbol extends BaseEntity {

    /** data/ 下的目录名，K 线靠它定位。全库唯一。 */
    @Setter
    @Column(nullable = false, unique = true)
    private String folder;

    /** 最小跳动（价格刻度）。 */
    @Setter
    private BigDecimal tick;

    /** 一个 tick 值多少美元。NULL / 0 表示没配置，调用方按「每点 1 美元」退化。 */
    @Setter
    private BigDecimal tickValue;

    /**
     * 手续费：<strong>每手每边</strong>多少美元（{@code CME_MINI:MES1!} = 0.50）。
     */
    @Column(nullable = false)
    private BigDecimal commission;

    private Symbol(String code) {
        useId(code);
    }

    /**
     * 按标的代码建一个还没填刻度的标的；<strong>手续费必须当场给出</strong>（{@code 0} = 免费）。
     *
     * @throws IllegalArgumentException 没给手续费
     */
    public static Symbol of(String code, String folder, BigDecimal commission) {
        Symbol symbol = new Symbol(code);
        symbol.folder = folder;
        symbol.setCommission(commission);
        return symbol;
    }

    /**
     * 改费率（已有标的调费率用这个，历史成交不受影响：成交存的是当时的快照）。
     *
     * @throws IllegalArgumentException 给了 {@code null} —— 「不知道多少」不是一个可以入库的状态
     */
    public void setCommission(BigDecimal commission) {
        if (commission == null) {
            throw new IllegalArgumentException("标的必须带手续费（可以为 0，但不能没有）: " + getId());
        }
        this.commission = commission;
    }

    /**
     * 单边手续费 = 每手每边费率 × 手数。费率不可能为空，所以这里不会有 {@code null}。
     *
     * <p>这里只算「成交一次」那一次：开仓调一次、平仓再调一次。往返请用
     * {@link #roundTripCommission(int)}，别在两个地方各乘一遍 2。
     */
    public BigDecimal perSideCommission(int contracts) {
        return commission.multiply(BigDecimal.valueOf(contracts));
    }

    /**
     * 往返手续费（开仓 + 平仓）= 单边 × 2。
     *
     * <p>为什么乘 2 写在这里而不是让调用方自己乘：本项目的手续费口径就是「单边收费」，
     * 一买一卖等于两笔手续费。把它收在实体里，「往返到底收几次」就只有一个答案；
     * 散在调用方那边迟早出现一处乘 2、一处忘乘 —— 而这种错不会抛异常，只会安静地少算一半成本。
     */
    public BigDecimal roundTripCommission(int contracts) {
        return perSideCommission(contracts).multiply(BigDecimal.valueOf(2));
    }
}
