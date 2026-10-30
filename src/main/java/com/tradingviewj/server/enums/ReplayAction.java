package com.tradingviewj.server.enums;

/**
 * 复盘里会发生的<strong>动作</strong>。目前用作「操作记录的动作」（{@code AuditEvent.action}）。
 *
 * <p>它曾经同时是交易记录里一步的类型：一步就是<strong>一条放行的广播</strong>
 * （点一次【下一根】、画一个图形、拖一次挂单价、录一段音，各占一步）。交易记录改成
 * <strong>引用 {@code audit_event} 的序号区间</strong>之后（2026-10-29），「一步」和
 * 「一条审计流水」终于是同一个东西了 —— 不需要再有一张步骤表来重复记一遍。
 *
 * <p>{@link #BASE} 由此获得了写入方：它是交易记录的<strong>第 0 步</strong>
 * （开仓之前那一屏），见 {@code AuditEvent#base}。{@link #RECORDING_AUDIO} 仍然没有写入方
 * —— 语音现在按 {@code audio_clip.bar_time} 落在区间内取，不需要单独占一条流水。
 * 两个取值都留着，是因为它们出现在已落库的历史审计负载里，删掉会让老数据读不回来。
 *
 * <p><strong>库里存 Java 常量名</strong>（{@code BAR_ADVANCED} / {@code ORDER_PLACED} …），
 * 靠字段上的 {@code @Enumerated(EnumType.STRING)}，没有转换器参与。
 *
 * <p>Go 协议里这些值是小写下划线串（{@code "bar_advanced"} / {@code "order_placed"} …），
 * 前端按它挑 reducer 分支。换算规则只有一条：<strong>常量名转小写</strong>
 * （{@code name().toLowerCase()}）。所以<strong>新增动作时不要用常量名区分不开的写法</strong>
 * —— 两个动作只要小写后撞了，前端就别想分清楚了。换算在输出侧做，不在这层。
 *
 * <p>{@code shortcut: 新增动作要先加枚举值}；读到库里不认识的常量名会直接抛异常
 * （宁可当场红，也不要悄悄写进一条解释不了的审计流水）。等「改动作名」真的成了日常，
 * 再考虑加 fallback 值。
 */
public enum ReplayAction {

    /** 推进一根 K 线。 */
    BAR_ADVANCED,
    /** 往后延伸一根真实成交过的 K 线（不产生任何成交）。 */
    BAR_EXTENDED,

    /** 画了一个图形。 */
    ANNOTATION_ADDED,
    /** 拖动图形端点。 */
    ANNOTATION_MOVED,
    /** 改已画好图形的样式（换模板），不动端点。 */
    ANNOTATION_STYLED,
    /** 删掉一个图形。 */
    ANNOTATION_DELETED,
    /** 一次清掉当前盘面的全部图形。 */
    ANNOTATION_CLEARED,

    /** 挂了一张单。 */
    ORDER_PLACED,
    /** 一张单成交了。 */
    ORDER_FILLED,
    /** 改了挂单价（画布上拖动挂单线）。 */
    ORDER_MOVED,
    /** 撤了一张单。 */
    ORDER_CANCELLED,
    /** 市价退仓 + 撤销全部挂单，一次动作换一条广播。 */
    FLAT_ALL,

    /** 接管权换人了。 */
    CONTROL_CHANGED,
    /** 连接的人变了。 */
    MEMBERS_CHANGED,

    /** 机器人启动。 */
    BOT_STARTED,
    /** 机器人停止。 */
    BOT_STOPPED,

    /** 录了一段语音。 */
    VOICE_ADDED,
    /** 清掉了某根 K 线上的语音。 */
    VOICE_CLEARED,
    /** 遗留：曾经是交易记录里的语音步（画面不动，但语音该响）。 */
    RECORDING_AUDIO,

    /** 交易记录的<strong>第 0 步</strong>：开仓**之前**那一屏。由 {@code AuditEvent#base} 写入。 */
    BASE
}
