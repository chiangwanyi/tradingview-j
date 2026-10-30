package com.tradingviewj.server.entity.trade;

import java.math.BigDecimal;
import java.math.RoundingMode;

/**
 * 金额的<strong>唯一</strong>归整口径：美元只到「分」。
 *
 * <p>为什么需要一个专门的类：交易域里每个金额都是从价格 × 手数 × 每点价值算出来的，
 * 而<strong>均价可能除不尽</strong>（六个价位的均价 174586 / 6 = 29097.6666…）。
 * 除不尽就会带出尾数，尾数再被手数放大，库里就会出现 {@code 834.99996} 这种
 * 「差 0.00004 就是整数」的脏数 —— 显示层 {@code toFixed(2)} 会把它盖成 835.00，
 * 但落库的值、API 的原始 JSON、余额累加全都被它污染。
 *
 * <p>所以口径必须只有一处：<strong>算完立刻收敛到分，之后谁都不许再产生更细的数</strong>。
 * 摆放位置选在 {@code entity.trade} 而不是 {@code service}，是因为
 * {@link Position} 和 {@link Trade} 都要用它，而实体层不该反向依赖服务层。
 *
 * <p>两位小数不是「够用就行」的近似：价格两位小数、手数最多四位、每点价值是 tick 的比值，
 * 真实结果本就是分的整数倍，四舍五入在这里<strong>不丢任何真值</strong>。
 */
public final class Money {

    /** 美元的最小单位：分。 */
    public static final int SCALE = 2;

    private Money() {
    }

    /** 收敛成可记账的金额（两位小数，四舍五入）。{@code null} 视为 0，免得每个调用点都判空。 */
    public static BigDecimal round(BigDecimal value) {
        if (value == null) {
            return BigDecimal.ZERO;
        }
        return value.setScale(SCALE, RoundingMode.HALF_UP);
    }
}
