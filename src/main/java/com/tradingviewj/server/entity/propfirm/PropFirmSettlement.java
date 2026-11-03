package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.entity.trade.Money;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;

/**
 * 一个交易日的<strong>判决书</strong>：这个账户在这一天收盘时账是什么样。
 *
 * <h2>为什么要有这张表</h2>
 *
 * <p>考核读数是「从阶段起点把成交和 K 线全部重放一遍」算出来的（{@code PropFirmJudge#assess}），
 * 而重放本身是<strong>没有跨请求游标</strong>的 —— 每次调用都从 {@code base_balance} 重新起算。
 * 正常情况下这没问题（重放是纯函数，同样的输入给同样的输出），但<strong>最后一天</strong>是个例外：
 *
 * <ul>
 *   <li>{@code PropFirmSession} 只在「跨交易日的第一根」结算日终，而盘面是手点推进的，
 *       用户推到某天最后一根就停住了，再没有一根 K 线来触发；</li>
 *   <li>于是 {@code PropFirmJudge#settleIfDayOver} 补了一刀：看紧挨着的下一根属不属于同一天，
 *       不属于就当场结算 —— <strong>用的余额是 {@code last_bar_time} 那一刻的</strong>。</li>
 * </ul>
 *
 * <p>问题就出在这句「那一刻」：{@code last_bar_time} 会变。同一份成交，停在 03:55 结算出
 * 50118，再推进到 04:00（日终强平）就变成 50088.5 —— <strong>EOD 峰值回落了 28.5</strong>，
 * 而亏损线 = 峰值 − 最大回撤，于是线也跟着回落。峰值本该只增不减，这里却成了
 * 「用户推到哪」的函数。2026-11-03 用交易机器人压测 6 户 × 300 根，刷出 61 条这种漂移，
 * 全部落在 03:55–05:55。
 *
 * <p>修法就是这张表：<strong>把结算从「每次重新求值的纯函数」变成「一次性落库的事实」</strong>。
 * 这一天结过账了，之后重放多少次都用这一行，不再重算。峰值于是天然单调 ——
 * 这正是「跟踪回撤」四个字的全部含义。
 *
 * <h2>峰值和余额为什么要分开存</h2>
 *
 * <p>{@link #eodBalance} 记的是「结算那一下看到的余额」，它可能<strong>比真正的收盘余额早</strong>
 * （用户停在 03:55 时，04:00 的日终强平还没发生），所以它<strong>不参与判定</strong>，只作审计；
 * 判定只看 {@link #peakBalance}（只增不减）。同一天被结算两次时（03:55 一次、推过 04:00 又一次），
 * 峰值取大的那个、余额原样保留第一次的 —— 账不因为「你多推了一根」而退回。
 *
 * <h2>为什么还要存判决</h2>
 *
 * <p>{@link #outcome} 是那天的结论（跑着 / 达标 / 失败）。结算一旦发生就不再重判，
 * 否则「达标那一刻」会随着后来的成交来回翻面：EVAL 收在 50088.5 刚过目标、次日回撤到线下，
 * 重放就会先判「没达标」再判「失败」，而当时真实发生的其实是「达标 → 冻结」。
 * 判决是历史，不是函数。
 *
 * <p>{@code accountId} 按 schema 第 4 条的例外处理：<strong>不加外键，但跟着账户一起删</strong>
 * （{@code PropFirmAccountService#delete}）。它和阶段流水一样记的是<strong>账户自己</strong>的时间轴，
 * 账户行一没就既无归属、也无入口可读。
 */
@Getter
@Entity
@Table(name = "pf_settlement")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmSettlement extends BaseEntity {

    @Column(nullable = false)
    private String accountId;

    /** 结算发生在哪个阶段。升级会重置余额基准，同一个交易日不能跨阶段复用。 */
    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private PropFirmStage stage;

    /** 交易日 {@code yyyy-MM-dd}（交易日的口径见 {@code PropFirmClock}，不是日文件名）。 */
    @Column(nullable = false)
    private String tradingDay;

    /** 结算那一刻的余额。<strong>只作审计</strong>：可能早于真正的收盘余额，不参与判定。 */
    @Column(nullable = false)
    private BigDecimal eodBalance;

    /** 截至这一日日终的 EOD 峰值。<strong>只增不减</strong>，亏损线由它推出来。 */
    @Column(nullable = false)
    private BigDecimal peakBalance;

    /** 截至这一日「单日净盈利最大值」的累计 —— 一致性上限靠它算目标。 */
    @Column(nullable = false)
    private BigDecimal maxDayNet;

    /** 截至这一日攒够的盈利日数（FUNDED 的升级条件）。 */
    @Column(nullable = false)
    private int profitDays;

    /** 这一天的判决。<strong>不重判</strong>，它就是历史。 */
    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Outcome outcome;

    /** 判决时给用户看的那句话（失败原因 / 还差多少）。 */
    private String message;

    /** 某一天结算结束时是「还在场上 / 达标冻结 / 撞线失败」。 */
    public enum Outcome {
        /** 还在场上。 */
        RUNNING,
        /** 满足升级条件，持仓已平、等用户点升级。 */
        PROMOTED,
        /** 撞了亏损线。 */
        FAILED
    }

    /**
     * 记下一天的结算结果。
     *
     * @param tradingDay 交易日（不是日文件名 —— 两者在「周六那份文件开头是周五的尾巴」时不同）
     */
    public static PropFirmSettlement record(String accountId, PropFirmStage stage, String tradingDay,
                                            BigDecimal eodBalance, BigDecimal peakBalance,
                                            BigDecimal maxDayNet, int profitDays,
                                            Outcome outcome, String message) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("结算记录必须属于一个 PF 账户，收到: " + accountId);
        }
        if (tradingDay == null || tradingDay.isBlank()) {
            throw new IllegalArgumentException("结算记录必须带交易日，收到: " + tradingDay);
        }
        PropFirmSettlement row = new PropFirmSettlement();
        row.accountId = accountId;
        row.stage = stage;
        row.tradingDay = tradingDay;
        row.eodBalance = Money.round(eodBalance);
        row.peakBalance = Money.round(peakBalance);
        row.maxDayNet = Money.round(maxDayNet);
        row.profitDays = profitDays;
        row.outcome = outcome == null ? Outcome.RUNNING : outcome;
        row.message = message;
        return row;
    }

    /**
     * 用「更晚一次结算」的余额把峰值补上去 —— <strong>只升不降</strong>，降的那一侧直接忽略。
     *
     * <h2>为什么需要补</h2>
     *
     * <p>结算点跟着「用户推进到的那一根」走：停在 03:55 时这一天就结了，可 03:55 之后还有
     * 04:00 那根上的<strong>日终强平</strong>。强平把余额抬上去（或打下来）之后，同一天会被再结一次 ——
     * 那一次才是真正的收盘余额。
     *
     * <p>2026-11-03 机器人压测就卡在这里：6 户 × 300 根剩下 49 条 {@code FLOOR_DRIFT}，
     * 追下来全都是「后端 51159.06 / 重算 51166.77」这种几块钱的差 —— 差的就是 04:00 强平那一笔。
     *
     * <p>所以规则定成这样：<strong>峰值取「该交易日所有已知结算点里最高的那个余额」</strong>，
     * 这本来就是 EOD 峰值的定义；{@code outcome} 不动（判决是历史，不因为收尾的一笔翻面）。
     *
     * @return 是否真的抬高了（没抬高就不用写库）
     */
    public boolean raiseTo(BigDecimal laterBalance) {
        BigDecimal candidate = Money.round(laterBalance);
        if (candidate.compareTo(peakBalance) <= 0) {
            return false;
        }
        this.eodBalance = candidate;
        this.peakBalance = candidate;
        return true;
    }
}
