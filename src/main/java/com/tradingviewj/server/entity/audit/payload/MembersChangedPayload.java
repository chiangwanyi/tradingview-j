package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 连接的人变了（{@link ReplayAction#MEMBERS_CHANGED}）。对应 Go 的 {@code MembersChangedPayload}。
 *
 * <p>带的是<strong>换完之后</strong>的完整名单，不是「谁进来了 / 谁走了」的增量：
 * 断线重连会让增量和真实状态对不上（同一个人可能连着两次「进来了」），而名单很短，
 * 整份替换永远是对的。
 *
 * <p>它和 {@link ControlChangedPayload} 是两条独立的动作 —— 有人进房间不等于换人操作，
 * 而换人操作也不一定有新连接。
 */
public record MembersChangedPayload(
        List<Member> members
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.MEMBERS_CHANGED;
    }
}
