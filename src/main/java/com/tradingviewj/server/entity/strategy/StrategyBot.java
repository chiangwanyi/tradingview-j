package com.tradingviewj.server.entity.strategy;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;
import java.time.Instant;

/**
 * 策略执行机器人：<strong>一次运行 = 一行</strong>。
 *
 * <p><strong>职责边界</strong>：它记录「谁在跑、跑完没、开了几单、为什么停」。
 * 它不存运行期的逐拍状态（当前持仓、当前交易日、挂着的两张保护单）——
 * 那些是进程内的东西，重启进程本来就不会接着跑。
 *
 * <p>为什么又落库：Go 版的机器人运行完全在内存里，进程一重启，「上次跑了多久、
 * 开了几单、是正常结束还是崩了」全部消失。落一行摘要的代价很小，
 * 而排查「今天这个策略为什么只开了 0 单」时，这是唯一的线索。
 *
 * <p>它<strong>不直接下单</strong>。所有成交都走和真人点按钮同一条路径
 * （{@code service} 里的撮合），所以跳空取开盘价、只撮合上一根之前的单，
 * 这些规矩一条都不会漏 —— 这正是策略不值得自己实现撮合的原因。
 */
@Getter
@Entity
@Table(name = "strategy_bot")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class StrategyBot extends BaseEntity {

    /** 在哪个 PF 账户上跑；NULL = 所在账户已被删除（运行摘要保留）。 */
    @Column
    private String accountId;

    /** NULL = 老式的随机演示机器人（不下真单，只演示操作）。 */
    @Column
    private String strategyId;

    /** 每笔手数。 */
    @Column(nullable = false)
    private BigDecimal quantity;

    /** 每根 K 线之间的间隔（毫秒）。 */
    @Column(nullable = false)
    private int intervalMs;

    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Status status;

    @Column(nullable = false)
    private Instant startedAt;

    private Instant stoppedAt;

    /** 这次运行一共开了几单。 */
    @Column(nullable = false)
    private int entryCount;

    /** 停止原因。 */
    @Enumerated(EnumType.STRING)
    private StopReason stopReason;

    /**
     * 启动一次运行。
     *
     * @param accountId 在哪个 PF 账户上跑，<strong>不能为空</strong>：机器人做的一切也要算在账上
     * @throws IllegalArgumentException 没给账户
     */
    public static StrategyBot start(String accountId, String strategyId, BigDecimal quantity, int intervalMs) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("策略机器人必须跑在某个 PF 账户上，收到: " + accountId);
        }
        StrategyBot bot = new StrategyBot();
        bot.accountId = accountId;
        bot.strategyId = strategyId;
        bot.quantity = quantity;
        bot.intervalMs = intervalMs;
        bot.status = Status.RUNNING;
        bot.startedAt = Instant.now();
        return bot;
    }

    /** 正常收工。重复调用无副作用（第一次之后状态已经不在 RUNNING 了）。 */
    public void stop(Status finalStatus, StopReason reason, int entryCount) {
        if (status != Status.RUNNING) {
            return;
        }
        this.status = finalStatus;
        this.stopReason = reason;
        this.entryCount = entryCount;
        this.stoppedAt = Instant.now();
    }

    public enum Status {
        RUNNING, STOPPED, FINISHED, FAILED
    }

    /**
     * 为什么停。
     *
     * <p>用枚举而不是自由文本：这一列将来要用来做筛选和统计（「数据跑到头的有几次、
     * 报错的有几次」），自由文本第一天就会写成「数据到头」「跑到末尾了」「EOF」三种。
     *
     * <p>注意它和 {@link Status} 是<strong>两个维度</strong>：Status 是「怎么结束的」，
     * StopReason 是「因为什么结束的」。一次 {@code FINISHED} 可能是跑完数据，
     * 也可能是用户点停前刚好平掉了最后一仓 —— 只看 Status 分不出来。
     */
    public enum StopReason {
        /** 用户点了停止。 */
        MANUAL,
        /** 数据跑到末尾（没有更多 K 线了）。 */
        DATA_END,
        /** 所在 PF 账户被删 / 关闭，机器人被动停（数据保留，只是不再有账户可挂）。 */
        ACCOUNT_CLOSED,
        /** 撮合或下单过程抛了异常。 */
        ERROR
    }
}
