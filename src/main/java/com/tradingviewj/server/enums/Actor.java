package com.tradingviewj.server.enums;

/**
 * 一次操作是<strong>谁</strong>做的。
 *
 * <p>这个枚举在四个地方用：{@code AuditEvent.actor}、{@code TradeRecordStep.actor}、
 * {@code AudioClip.actor}、{@code TradeRecord.actor}。它们问的是同一个问题
 * 「这些动作是谁做的」，所以是<strong>一个</strong>枚举而不是四个取值相同的枚举 ——
 * 取值相同的多个枚举只会带来「转来转去」的代码，而且迟早有一边新增了取值另一边没跟上。
 *
 * <p><strong>库里存 Java 常量名</strong>（{@code HUMAN} / {@code BOT} / {@code SYSTEM}），
 * 靠字段上的 {@code @Enumerated(EnumType.STRING)}，没有转换器参与。持久化层不为展示格式让步：
 * 枚举名就是这个枚举的意思，不需要为迁就某个客户端换个写法再翻回来。
 *
 * <p>协议里前端读的是小写串（{@code "human"} / {@code "bot"} / {@code "system"}，即 Go 侧的
 * {@code by} 字段）。换算规则只有一条：<strong>常量名转小写</strong>（{@code name().toLowerCase()}）。
 * 这个换算属于输出侧（DTO 或前端），不在这层做。
 */
public enum Actor {

    /** 真人手动操作。 */
    HUMAN,
    /** 机器人（策略或随机演示）操作。 */
    BOT,
    /** 系统自己动的（自动收尾、崩溃后重启补录这类）。 */
    SYSTEM
}
