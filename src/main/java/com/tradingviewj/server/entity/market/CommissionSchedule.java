package com.tradingviewj.server.entity.market;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.CollectionTable;
import jakarta.persistence.Column;
import jakarta.persistence.ElementCollection;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.MapKeyColumn;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;

/**
 * 手续费价目表：<strong>全库只有一行</strong>。
 *
 * <p>对应 {@code data/_symbols.json} 里新增的 {@code commission} 段：
 *
 * <pre>
 * "commission": {
 *   "type": "PER_CONTRACT_PER_SIDE",
 *   "rates": { "MES": 0.50, "MNQ": 0.50, "MGC": 0.80 }
 * }
 * </pre>
 *
 * <p><strong>为什么是一张全局表，而不是 {@link Symbol} 上的两列</strong>：
 * 这份 JSON 的语义就是「一套计费口径 + 一张按品种查的价目表」——
 * {@code type} 只有一份（不按品种分别规定），只有 {@code rate} 按品种变。
 * 做成 Symbol 的 {@code @Embedded} 会把同一个 type 复制 N 份（N = 标的数），
 * 改口径时得改 N 行，而且「有品种漏改」这种错在数据里看不出来。
 *
 * <p><strong>键是「品种根名」不是标的代码</strong>：JSON 的 rates 键是 {@code MES}，
 * 而标的代码是 {@code CME_MINI:MES1!}。两者用 {@link #rootOf(String)} 对齐：
 * 取冒号后面、连续字母那一段（{@code CME_MINI:MES1!} → {@code MES}）。
 * 查表时不区分大小写，先按整串比、再按根名比，都查不到返回 {@code null}
 * （= 这只标的没配手续费，<strong>不是 0</strong>：0 会让「漏配」和「免手续费」混成一种）。
 *
 * <p><strong>本类只做「查价」和「单价 × 手数」两件事，不碰账</strong>：
 * 手续费进不进 {@code TradeLedger}、往返是否乘 2、和已实现盈亏是净额还是总额并列，
 * 这些是业务口径，{@code TradeLedger} 那边确认后再实现。
 * 按 AGENTS.md，实体里不允许「顺手」算一个。
 *
 * <p><strong>⚠️ JSON 里这一段目前是「单方面」的</strong>：Go 抓取侧不读 {@code commission}
 * （多一个未知键它照常忽略，不会报错），但它在分配新标的文件夹时会<strong>按自己的结构整个重写
 * {@code _symbols.json}</strong>（{@code internal/store/store.go: saveIndexLocked}，只写
 * {@code version} + {@code symbols}）—— 那一刻 {@code commission} 段会被静默抹掉。
 * 触发条件是「出现一个索引里没有的新标的」。所以这份费率真正的落点必须是本表（库），
 * JSON 只当人工维护的初始值。
 */
@Getter
@Entity
@Table(name = "commission_schedule")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class CommissionSchedule extends BaseEntity {

    /**
     * 固定主键：价目表是全局唯一的一份，不给它随机 UUID
     * —— 有随机 id 就会出现「库里两行、运行期用哪一行看运气」。
     */
    public static final String SINGLETON_ID = "commission";

    /** 计费口径。所有品种共用一套。 */
    @Enumerated(EnumType.STRING)
    @Column(name = "type", nullable = false)
    private FeeType type;

    /**
     * 品种根名 → 每手每边的手续费（美元）。
     *
     * <p>金额是 {@code BigDecimal}，库里是 TEXT（{@code BigDecimalTextConverter}）：
     * 手续费要累加成月成本，用 double 累几千笔之后小数位就对不上了。
     * 用 {@code LinkedHashMap} 只为落库顺序稳定、便于人眼对照 JSON。
     */
    @ElementCollection(fetch = FetchType.EAGER)
    @CollectionTable(name = "commission_rate", joinColumns = @JoinColumn(name = "schedule_id"))
    @MapKeyColumn(name = "symbol_root")
    @Column(name = "rate", nullable = false)
    private Map<String, BigDecimal> rates = new LinkedHashMap<>();

    private CommissionSchedule(FeeType type) {
        useId(SINGLETON_ID);
        this.type = type;
    }

    /** 建一份价目表（还没填任何品种的费率）。 */
    public static CommissionSchedule of(FeeType type) {
        return new CommissionSchedule(type);
    }

    /** 登记一个品种的费率。品种名写成 {@code MES} 或 {@code CME_MINI:MES1!} 都可以，落库统一成根名。 */
    public CommissionSchedule withRate(String symbol, BigDecimal ratePerContractPerSide) {
        rates.put(rootOf(symbol), ratePerContractPerSide);
        return this;
    }

    /**
     * 查一只标的的单边每手手续费。查不到返回 {@code null}。
     *
     * <p>顺序是「整串完全匹配 → 根名匹配」，这样将来真要给同根的两个合约（不同到期月）配不同价，
     * 直接把完整代码写进 JSON 就能盖掉根名的价，不用改代码。
     */
    public BigDecimal rateFor(String symbolCode) {
        if (symbolCode == null || symbolCode.isBlank()) {
            return null;
        }
        String code = symbolCode.trim();
        BigDecimal exact = rates.get(code);
        if (exact != null) {
            return exact;
        }
        String root = rootOf(code);
        for (Map.Entry<String, BigDecimal> e : rates.entrySet()) {
            if (e.getKey() != null && e.getKey().trim().equalsIgnoreCase(root)) {
                return e.getValue();
            }
        }
        return null;
    }

    /**
     * 单边手续费 = 每手每边费率 × 手数。查不到费率返回 {@code null}。
     *
     * <p>{@code PER_CONTRACT_PER_SIDE} 的口径就是「成交一次算一次」：开仓算一次、平仓再算一次。
     * 所以这里只算单边，往返请调两次 —— 本类不替你决定「平仓到底算不算一次」。
     */
    public BigDecimal perSideFee(String symbolCode, int contracts) {
        BigDecimal rate = rateFor(symbolCode);
        if (rate == null) {
            return null;
        }
        return rate.multiply(BigDecimal.valueOf(contracts));
    }

    /**
     * 标的代码 → 品种根名：{@code CME_MINI:MES1!} → {@code MES}、{@code NYMEX:CL1!} → {@code CL}。
     *
     * <p>规则很笨但可预测：丢掉冒号前那一段（交易所前缀），再取后面**开头连续的那段字母**。
     * 于是 {@code 1!} / {@code 2!} 这类月份后缀、连续合约标记都被自然扔掉。
     * 数字开头的品种（如 {@code 6E}）保留整串，不至于因为规则猜错就查不到。
     */
    public static String rootOf(String symbolCode) {
        String s = symbolCode == null ? "" : symbolCode.trim();
        int colon = s.indexOf(':');
        if (colon >= 0) {
            s = s.substring(colon + 1);
        }
        int end = 0;
        while (end < s.length() && Character.isLetter(s.charAt(end))) {
            end++;
        }
        String root = end == 0 ? s : s.substring(0, end);
        return root.toUpperCase(Locale.ROOT);
    }

    /**
     * 计费口径。目前只有一种，但仍然是枚举而不是 {@code String}：
     * 将来加「按名义金额百分比」「往返一口价」时，取值域是封闭的，
     * 写成字符串会出现 {@code per_contract} / {@code PER_CONTRACT} 两种拼法。
     */
    public enum FeeType {
        /** 按手数收费，每手每边一个固定金额（MES 0.50 / MNQ 0.50 / MGC 0.80）。 */
        PER_CONTRACT_PER_SIDE
    }
}
