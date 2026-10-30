package com.tradingviewj.server.entity.audit.payload;

/**
 * 实例里的一个人（支撑类型，不是动作）。对应 Go 的 {@code Member}。
 *
 * <p>只出现在 {@link MembersChangedPayload} 里：{@code operator} 是「现在谁在操作」，
 * {@code connected} 是「还在不在线」（断线但没退出的人仍留在名单上，只是灰掉）。
 *
 * <p><strong>它不落库</strong>：成员是进程内的连接状态，重启即重新握手 —— 所以 Java 侧
 * 没有对应的实体表，这里只是把 wire 上的形状说清楚。
 */
public record Member(
        String clientId,
        String name,
        boolean operator,
        boolean connected
) {
}
