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
 *
 * <p><strong>职责边界</strong>：它是「一个可以交易的品种」的身份 + 它的价格刻度 + 它的手续费。
 * 它<strong>不持有 K 线</strong> —— 行情在 CSV 里，{@code storage.CsvBarStore} 按
 * {@link #getFolder()} 去找目录。它也不持有任何交易或账户数据。
 *
 * <p>主键就是标的代码（{@code CME_MINI:MES1!}），不是 UUID：这个代码是 TradingView
 * 里的品种标识，人眼可读、天然唯一，用 UUID 存反而每次都要多查一层。
 *
 * <p><strong>相对 Go 版的关键改动</strong>：Go 的 {@code Instance} 上各存一份
 * {@code Tick} / {@code TickValue}（建实例时抄过去），于是同一个标的在 20 个实例里
 * 有 20 份可能不一致的刻度。这里刻度只存在于 Symbol 一处，实例要用就去查它。
 *
 * <p>注意 {@code tickValue} 是<strong>一跳值多少美元</strong>，不是一跳多粗的价格。
 * 光有 tick 只知道价格跳多粗（0.25），不知道一跳值多少钱（MNQ 0.50 / MES 1.25 / CL 10.00），
 * 算盈亏必须用它。它是用户手填的，不是算出来的。
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
     *
     * <p>它是标的自己的参数，跟着标的走 —— 没有单独的价目表，也没有「全局计费口径」这一层。
     * 计费口径是<strong>内建的</strong>：按手数收费、单边收费（见 {@link #roundTripCommission(int)}），
     * 所以一条交易的开仓和平仓各收一次。
     *
     * <p><strong>只有两个值：0 = 免费，&gt;0 = 收费。没有 NULL</strong>。
     * 这一列 NOT NULL，建标的那一刻就必须给出费率（{@link #of}），之后改费率走
     * {@link #setCommission(BigDecimal)}，同样收不了 null。为什么不再留一个「还没配」：
     * 三态（没配 / 免费 / 收费）看起来更严谨，实际用法是漏配的标的安静地按免费结算，
     * 账面上完全看不出来；而 {@code NYMEX:CL1!} 那种免手续费是<strong>明确写下 0</strong>
     * 的事实，不是省略出来的默认值。
     *
     * <p>金额是 {@code BigDecimal}，库里存 TEXT（{@code BigDecimalTextConverter}）：
     * 手续费要按月累加几千笔，用 double 会攒出小数位的偏差。
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
