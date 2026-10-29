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
     * <p><strong>NULL 和 0 不是一回事</strong>：NULL = 这只标的还没配（{@code data/_symbols.json}
     * 里那一行没有 {@code commission} 键），0 = 明确写了 {@code "commission": 0}，也就是免手续费
     * （{@code NYMEX:CL1!} 就是这样）。调用方必须区分「没配」和「免费」，
     * 否则漏配的标的会安静地按免手续费结算。
     *
     * <p>金额是 {@code BigDecimal}，库里存 TEXT（{@code BigDecimalTextConverter}）：
     * 手续费要按月累加几千笔，用 double 会攒出小数位的偏差。
     */
    @Setter
    private BigDecimal commission;

    private Symbol(String code) {
        useId(code);
    }

    /** 按标的代码建一个还没填刻度的标的。 */
    public static Symbol of(String code, String folder) {
        Symbol symbol = new Symbol(code);
        symbol.folder = folder;
        return symbol;
    }

    /**
     * 单边手续费 = 每手每边费率 × 手数。没配手续费返回 {@code null}。
     *
     * <p>这里只算「成交一次」那一次：开仓调一次、平仓再调一次。往返请用
     * {@link #roundTripCommission(int)}，别在两个地方各乘一遍 2。
     */
    public BigDecimal perSideCommission(int contracts) {
        if (commission == null) {
            return null;
        }
        return commission.multiply(BigDecimal.valueOf(contracts));
    }

    /**
     * 往返手续费（开仓 + 平仓）= 单边 × 2。没配手续费返回 {@code null}。
     *
     * <p>为什么乘 2 写在这里而不是让调用方自己乘：本项目的手续费口径就是「单边收费」，
     * 一买一卖等于两笔手续费。把它收在实体里，「往返到底收几次」就只有一个答案；
     * 散在调用方那边迟早出现一处乘 2、一处忘乘 —— 而这种错不会抛异常，只会安静地少算一半成本。
     */
    public BigDecimal roundTripCommission(int contracts) {
        BigDecimal side = perSideCommission(contracts);
        if (side == null) {
            return null;
        }
        return side.multiply(BigDecimal.valueOf(2));
    }
}
