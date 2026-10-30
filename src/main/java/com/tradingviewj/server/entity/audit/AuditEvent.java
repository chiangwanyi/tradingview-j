package com.tradingviewj.server.entity.audit;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.enums.ReplayAction;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 一条操作记录。**只追加，永不修改、永不删除**。
 *
 * <h2>它是「日志」，不是「状态」</h2>
 *
 * <p>职责边界：它回答「<strong>谁、在哪根 K 线上、游标第几根、做了什么</strong>」。
 * 它<strong>不是</strong>业务状态的来源 —— 图形、挂单、成交各自有自己的表。
 * 这条分界不能破：如果靠重放操作记录来还原状态，那么一次漏写就会让整块盘面的状态
 * 永久错位；而独立存表的话，最坏情况是「审计流水少了一条」，盘面本身还是对的。
 *
 * <p>反过来，这条日志确实<strong>能</strong>独立回答两件别处答不出来的事：
 *
 * <ol>
 *   <li><strong>操作的先后顺序</strong>（含同一根 K 线里的多次操作）。成交有
 *       {@code Trade.seq}，但「先画了线再挂单」这种盘面动作只有这里按 {@link #seq} 记着。</li>
 *   <li><strong>用户看到第几根</strong>：最后一条事件的 {@link #barTime}。
 *       这一列是 2026-10-29 的答案 —— {@code replay_cursor} 表当天删了，进度不再单独存；
 *       中间曾经加过一个 {@code cursor}（第几根的下标），同一天又删了：下标是位置，
 *       文件内容一变就指向另一根 K 线，而时间是标识。</li>
 * </ol>
 *
 * <h2>为什么 {@link #symbolId} / {@link #barTime} 抽成真列</h2>
 *
 * <p>它们本来只在 {@link #payloadJson} 里，靠解析 JSON 也能拿到。抽出来的理由是
 * <strong>查询</strong>：一个账户可以同时做 MES / MNQ / MGC，而 {@link #seq} 是账户内
 * 全局连续的 —— 没有 {@code symbol_id}，「这个品种从第几步到第几步」就只能把整个账户的
 * 流水拉回内存再逐条解析。逐 K 回放每次打开记录都要做这件事，不能这么做。
 *
 * <h2>payload 为什么是字符串</h2>
 *
 * <p>{@link #payloadJson} 存原始广播负载的 JSON 文本，而不是像 Go 版那样用
 * {@code any}（Java 里是 {@code Object}）。用 {@code Object} 的代价是：反序列化时
 * 不知道该还原成哪个类，只能全存成 {@code Map}，读的时候到处强转、到处判空。
 * 存字符串 + 一个类型化的 {@link #action}，读的一方按 action 自己挑 DTO 解析 ——
 * 「哪个 action 对应哪一类」的唯一清单在
 * {@link com.tradingviewj.server.entity.audit.payload.ReplayActionPayload}（一个动作一个 record）。
 *
 * <p>两个枚举字段（{@link #actor} / {@link #action}）在库里存的是 <strong>Java 常量名</strong>
 * （{@code HUMAN} / {@code BAR_ADVANCED}）。协议上的小写串由输出侧映射，持久化不跟展示格式走 ——
 * 见 {@link com.tradingviewj.server.enums.Actor} 的说明。
 */
@Getter
@Entity
@Table(name = "audit_event")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AuditEvent extends BaseEntity {

    /**
     * 属于哪个 PF 账户。<strong>必有值</strong>，没有「不属于任何账户的操作」。
     *
     * <p><strong>故意不加外键</strong>，和别的表不一样：账户被删之后这里仍然保留
     * 原来的 id。理由是「只追加」—— 任何 {@code ON DELETE} 动作都是一次改写
     * （{@code SET NULL} 改归属、{@code CASCADE} 删行），而审计流水的全部价值
     * 就在于它没被改过。指向一个已经不存在的账户 id 不丢任何信息，
     * 反而记住了「当初算在谁头上」。
     *
     * <p>代价：原本用 NULL 表达的「全局操作」（例如新建标的）现在必须有归属。
     * 需要这类操作时，应当新建一个专门的账户来承载，而不是把这一列放开。
     */
    @Column(nullable = false)
    private String accountId;

    /**
     * 这一步发生在哪个品种的盘面上。
     *
     * <p>不是从账户推的：账户可以同时做 MES / MNQ / MGC，同一个 {@link #seq} 空间里
     * 各品种的操作是混着排的。逐 K 回放要按品种过滤，靠这一列。
     *
     * <p>NOT NULL：所有动作都发生在某块盘面上，包括「退出」—— 退出时盘面停在某一根 K 线，
     * 那根的时间是有的。如果将来真出现一个没有盘面的动作（例如「改账户名」），
     * 它不该混进这张表，而是另开一张。
     */
    @Column(nullable = false)
    private String symbolId;

    /** 这一步发生时，盘面停在哪根 K 线上（本地时区明文，和 CSV 一致）。 */
    @Column(nullable = false)
    private String barTime;

    /** 账户内的自增序号，从 0 起。断档就说明某次广播没落库。 */
    @Column(nullable = false)
    private int seq;

    /** 谁做的。 */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Actor actor;

    /** 做了什么。 */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private ReplayAction action;

    /** 这次操作的原始负载（JSON 文本）。起点事件没有负载，见 {@link #base}。 */
    @Column
    private String payloadJson;

    /**
     * 记一条操作流水。
     *
     * @param accountId 算在哪个 PF 账户头上，<strong>不能为空</strong>
     * @param symbolId  发生在哪个品种的盘面上，<strong>不能为空</strong>
     * @param barTime   当时停在哪根 K 线上，<strong>不能为空</strong>
     * @param seq       账户内序号，<strong>不能为负</strong>（从 0 起）
     * @throws IllegalArgumentException 缺账户 / 品种 / K 线时间，或序号为负
     */
    public static AuditEvent of(String accountId, String symbolId, String barTime,
                                int seq, Actor actor, ReplayAction action, String payloadJson) {
        requireText(accountId, "操作记录必须归属一个 PF 账户");
        requireText(symbolId, "操作记录必须发生在某个标的的盘面上");
        requireText(barTime, "操作记录必须记着当时停在哪根 K 线上");
        if (seq < 0) {
            throw new IllegalArgumentException("操作记录的序号从 0 起，收到: " + seq);
        }
        AuditEvent event = new AuditEvent();
        event.accountId = accountId;
        event.symbolId = symbolId;
        event.barTime = barTime;
        event.seq = seq;
        event.actor = actor;
        event.action = action;
        event.payloadJson = payloadJson;
        return event;
    }

    /**
     * 交易记录的<strong>第 0 步</strong>：开仓之前那一屏。
     *
     * <p>{@link ReplayAction#BASE} 是一个特殊取值：它不是「用户做了什么」，而是
     * 「回放从这里开始」的定位点。{@code TradeRecord.startSeq} 就指着它。
     *
     * <p><strong>它不带盘面快照。</strong>Go 版把整份盘面（K 线 + 图形 + 挂单 + 成交 + 持仓）
     * 塞进了 Base 里，等于同一份盘面存两遍。这里不复制：起点盘面由
     * 「{@code Drawing} / {@code AudioClip} / {@code Order} / {@code Trade} 按 {@link #barTime}
     * 及之前过滤」现算 —— 代价是还原的是<strong>现在</strong>的样子（图形被拖动过就看不到原来的），
     * 那是「图形不做版本化」的已知边界，不是这里省事省出来的。
     */
    public static AuditEvent base(String accountId, String symbolId, String barTime,
                                  int seq, Actor actor) {
        return of(accountId, symbolId, barTime, seq, actor, ReplayAction.BASE, null);
    }

    /** 是不是交易记录的起点事件。 */
    public boolean isBase() {
        return action == ReplayAction.BASE;
    }

    private static void requireText(String value, String message) {
        if (value == null || value.isBlank()) {
            throw new IllegalArgumentException(message + "，收到: " + value);
        }
    }
}
