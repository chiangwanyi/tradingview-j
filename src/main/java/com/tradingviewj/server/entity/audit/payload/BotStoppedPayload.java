package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

import java.math.BigDecimal;

/**
 * 机器人停止（{@link ReplayAction#BOT_STOPPED}）。对应 Go 的 {@code BotPayload}。
 *
 * <p>字段和 {@link BotStartedPayload} 完全相同，但语义是「跑完了，它是怎么跑的」：
 * 停的那一刻把这次运行的节奏和手数再报一遍，操作记录里才读得出「它跑了多久一根、
 * 每笔几手」—— 只有「机器人已停止」这五个字的话，事后再看什么都推不出来。
 *
 * <p>停止时<strong>不平仓</strong>：用户可能只是想暂停、自己接手接着做，界面上的持仓和盈亏
 * 都还在。被撤掉的只有这次运行挂的保护单（不撤就会在盘面上越积越多）。
 */
public record BotStoppedPayload(
        int intervalMs,
        String strategy,
        String name,
        BigDecimal quantity
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.BOT_STOPPED;
    }
}
