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
 * <p><strong>职责边界</strong>：它是「一个可以交易的品种」的身份 + 它的价格刻度。
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

    private Symbol(String code) {
        useId(code);
    }

    /** 按标的代码建一个还没填刻度的标的。 */
    public static Symbol of(String code, String folder) {
        Symbol symbol = new Symbol(code);
        symbol.folder = folder;
        return symbol;
    }
}
