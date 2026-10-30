package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

import java.math.BigDecimal;

/**
 * 机器人启动（{@link ReplayAction#BOT_STARTED}）。对应 Go 的 {@code BotPayload}。
 *
 * <p>{@code strategy} 为空表示老式的<strong>随机演示</strong>机器人（只会乱点乱下单）；
 * 非空时是策略 id，{@code name} 是给人看的名字。前端靠这两个字段把「启动机器人」按钮
 * 改成「停止 Caveman」—— 不知道自己停的是谁，那个按钮就不敢点。
 *
 * <p>{@code quantity} 是每次开仓的手数（空/0 = 按策略自己的默认值）。它一起发下来，
 * 是因为「同一个策略这次跑 3 手」是这次运行的属性，重启之后界面还得能说清。
 *
 * <p>启动机器人会顺带广播一条 {@link ControlChangedPayload}（操作权交给 {@code "bot"}），
 * 所以这条负载里<strong>不再重复</strong>操作者。
 */
public record BotStartedPayload(
        int intervalMs,
        String strategy,
        String name,
        BigDecimal quantity
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.BOT_STARTED;
    }
}
