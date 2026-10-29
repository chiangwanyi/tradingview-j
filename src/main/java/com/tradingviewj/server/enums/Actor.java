package com.tradingviewj.server.enums;

/**
 * 一次操作是<strong>谁</strong>做的。
 *
 * <p>这个枚举在四个地方用：{@code AuditEvent.actor}、{@code TradeRecordStep.actor}、
 * {@code AudioClip.actor}、{@code TradeRecord.actor}。它们问的是同一个问题
 * 「这些动作是谁做的」，所以是<strong>一个</strong>枚举而不是四个取值相同的枚举 ——
 * 取值相同的多个枚举只会带来「转来转去」的代码，而且迟早有一边新增了取值另一边没跟上。
 *
 * <p><strong>为什么带一个 {@link #wire()} 而不是直接用 {@code EnumType.STRING}</strong>：
 * 直接存 STRING 会把常量名 {@code HUMAN} 写进库，而协议里前端一直读的是小写
 * {@code "human"} / {@code "bot"}（Go 的 {@code by} 字段）。一旦哪天把操作记录下发给前端，
 * 值域就对不上了。所以枚举名给 Java 看、{@link #wire()} 给库和协议看，
 * 由 {@code converter.ActorConverter} 在边界上转。
 */
public enum Actor {

    /** 真人手动操作。 */
    HUMAN("human"),
    /** 机器人（策略或随机演示）操作。 */
    BOT("bot"),
    /** 系统自己动的（自动收尾、崩溃后重启补录这类）。 */
    SYSTEM("system");

    private final String wire;

    Actor(String wire) {
        this.wire = wire;
    }

    /** 落库 / 上协议时用的值。 */
    public String wire() {
        return wire;
    }

    public static Actor ofWire(String wire) {
        for (Actor actor : values()) {
            if (actor.wire.equals(wire)) {
                return actor;
            }
        }
        throw new IllegalArgumentException("未知的操作者: " + wire);
    }
}
