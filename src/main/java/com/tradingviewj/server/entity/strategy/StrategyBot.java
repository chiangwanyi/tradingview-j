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

    @Column(nullable = false)
    private String sessionId;

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

    /** 停止原因（数据到头 / 手动 / 异常信息）。 */
    private String stopReason;

    public static StrategyBot start(String sessionId, String strategyId, BigDecimal quantity, int intervalMs) {
        StrategyBot bot = new StrategyBot();
        bot.sessionId = sessionId;
        bot.strategyId = strategyId;
        bot.quantity = quantity;
        bot.intervalMs = intervalMs;
        bot.status = Status.RUNNING;
        bot.startedAt = Instant.now();
        return bot;
    }

    /** 正常收工（数据跑完 / 用户点停）。重复调用无副作用。 */
    public void stop(Status finalStatus, String reason, int entryCount) {
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
}
