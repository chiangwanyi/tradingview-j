package com.tradingviewj.server.entity.record;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import com.tradingviewj.server.entity.trade.Money;
import com.tradingviewj.server.entity.trade.Side;
import jakarta.persistence.CollectionTable;
import jakarta.persistence.Column;
import jakarta.persistence.ElementCollection;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.OrderColumn;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

/**
 * 交易记录：<strong>一段事件流 + 一份结算摘要</strong>。
 *
 * <p>名字不叫 recording / 录像：它确实不是视频，是「这一笔交易的事实摘要」。
 *
 * <h2>它是 {@code trade} 的一个区间投影（2026-10-30 起；之前锚在 {@code audit_event.seq}）</h2>
 *
 * <p>记录<strong>不复制</strong>成交，只引用一段：{@link #startSeq} 到 {@link #endSeq} 之间的
 * 所有 {@code trade}（同一账户、按 {@code seq} 升序）就是这笔交易的全过程。
 *
 * <p><strong>为什么从事件序号换成成交流水号</strong>：撮合成交并进 {@code bar_advanced}，
 * 没有自己的 {@code audit_event} 行 —— 两个 {@code seq} 序列本来就不对齐，用事件 seq
 * 切不到「第几笔成交」。而记录的边界（仓位穿越 0）永远落在一笔成交上，所以锚点只能是
 * {@code trade.seq}：它每笔必有、账户内单调递增。
 *
 * <p>反手会让<strong>相邻两条记录共享同一个 seq</strong>（旧记录的 {@code endSeq} =
 * 新记录的 {@code startSeq}）：一笔成交在事实上就是「平掉旧仓 + 开新仓」两条腿，
 * 而 {@code TradeLedger.realizedFor} 只把平仓那半算进这笔的盈亏。
 *
 * <p>为什么是「区间」而不是「自带步骤表」：
 *
 * <ul>
 *   <li><strong>不存两遍。</strong> Go 版的 {@code Recording} 自带 steps，同时实例存档里
 *       还有一份一模一样的盘面 —— 同一份事实存两处，只能靠纪律保证不分叉。</li>
 *   <li><strong>删除天然安全。</strong> 摘要（盈亏、MFE/MAE、手数）是独立列，不依赖成交行。
 *       账户被删、成交行将来被清理，记录本身照样能列出来、能看盈亏 ——
 *       这正是「删账户后仍能在另一个页面看所有记录」这条需求的落点。</li>
 *   <li><strong>顺序只有一个来源。</strong> {@code trade.seq} 是账户内唯一的偏序，
 *       记录里再存一份步号就成了第二个。</li>
 * </ul>
 *
 * <p>代价要说清楚：记录和成交的绑定是<strong>数字区间</strong>，
 * 所以 {@code trade} 的清理策略必须尊重这个区间 —— <b>不能按时间窗口只删中间一段</b>，
 * 否则记录会指向一段空洞。真要清理，得先决定成交的留存年限，那是另一个决定。
 *
 * <p><strong>职责边界</strong>：
 *
 * <ul>
 *   <li>摘要字段（方向、进出价、手数、盈亏、MFE/MAE）是<strong>收尾时算好写进来的</strong>。
 *       它们冗余于 {@code trade} 表，但值得冗余：交易记录列表要按盈亏排序、按标签筛选，
 *       每次列表都去重放几百笔成交是不可接受的。写入是一次性的，之后这笔交易就封存了。</li>
 *   <li>它<strong>不持有成交</strong>。成交在 {@code trade} 表里，靠 {@code trade.trade_record_id}
 *       反向引用 —— 因为成交先发生、记录后收尾，硬做成「记录包含成交」就需要先有记录再有成交，
 *       而用户是直接下单的。</li>
 *   <li>它不持有音频。语音是挂在 K 线上的独立实体，和记录没有从属关系；
 *       回放时语音由 {@code audio_clip} 按 {@code bar_time} 落在区间内取出来。</li>
 * </ul>
 *
 * <p><strong>生命周期是自动的</strong>：仓位从空仓变成有仓那一刻开始，回到空仓那一刻收尾。
 * 让用户记得点「开始/结束」的结果必然是「忘了开就没有记录」。所以没有「录制中」这个状态，
 * 也没有开关。
 *
 * <p>{@link #accountId} 只是<strong>统计归属</strong>，不是所有权：账户被删掉之后这里仍然保留
 * 原来那个 id（指向一个已经不存在的账户），打开、回放、删除记录都不依赖账户 ——
 * 记录是独立的一份数据，只有用户自己点删除才会消失。
 */
@Getter
@Entity
@Table(name = "trade_record")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class TradeRecord extends BaseEntity {

    /**
     * 属于哪个 PF 账户。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：和成交、图形一样是<strong>弱引用</strong>，
     * 账户只是统计点，记录不归它所有。删账户之后这条记录仍在，这里保留原来那个 id；
     * 想改归属用 {@link #attachTo} 显式挂到另一个账户上。
     */
    @Column(nullable = false)
    private String accountId;

    /**
     * 账户名的<strong>快照</strong>（记下开这笔交易时账户叫什么）。
     *
     * <p>不这么做的话，「删账户之后在另一个页面看所有记录」会得到一片空白：
     * {@link #accountId} 指向的那个账户行已经不在了，名字无处可查。这和
     * {@code Trade.commission} 存费率快照是同一个理由 —— 归属的<strong>名字</strong>
     * 也是「签下那一刻的事实」，账户改名不该让历史记录的归属跟着改。
     *
     * <p>所以它<strong>不跟随</strong>账户改名：改完名之后旧记录仍显示旧名字，
     * 这是有意的。要让某条记录显示新名字，只能显式 {@link #attachTo} 重新挂一次。
     */
    @Column(nullable = false)
    private String accountName;

    /** 这笔交易做的是哪个品种。账户可以多品种，{@link #anchorBarTime} 只说明"哪一根"。 */
    @Column(nullable = false)
    private String symbolId;

    /**
     * 起点那一屏的 K 线时间（开仓**之前**）。
     *
     * <p>和开仓那一笔成交的 {@code trade.bar_time} 是同一个值 ——
     * 冗余存一份，是为了「记录列表 / 详情页」不必为了显示时间去查一次成交表。
     * 这类冗余是安全的：它不可变，写入即封存。
     */
    @Column(nullable = false)
    private String anchorBarTime;

    /**
     * 这笔交易在 {@code trade} 里的<strong>起点流水号</strong>，指向开仓那一笔成交的
     * {@code trade.seq}。
     *
     * <p>{@code updatable = false}：区间起点一旦定下就不再改。
     * 交易是「空仓 → 有仓」那一刻开始的，那个时刻不可能事后挪动。
     *
     * <p>反手时它<strong>等于</strong>上一条记录的 {@link #endSeq}：同一笔成交既是旧仓的
     * 最后一腿、也是新仓的第一腿。
     */
    @Column(nullable = false, updatable = false)
    private int startSeq;

    /**
     * 这笔交易在 {@code trade} 里的<strong>终点流水号</strong>；{@code -1} = 还没收尾。
     *
     * <p>收尾那一刻（仓位回到 0）写下，之后不再改。{@code -1} 不是「坏了」：
     * 账户被删、程序退出、进程崩溃时仓位确实还在，那条记录就是「当时还没结束」——
     * 和 {@link #closed} 表达的是同一件事的两种粒度（一个是布尔、一个是精确到第几笔）。
     */
    @Column(nullable = false)
    private int endSeq = -1;

    @Setter
    private String entryBarTime;

    @Setter
    private String exitBarTime;

    @Setter
    @Enumerated(EnumType.STRING)
    private Side side;

    @Setter
    private BigDecimal quantity;

    /** 持仓期间的最大手数（加减仓之后的峰值）。 */
    @Setter
    private BigDecimal maxQuantity;

    @Setter
    private BigDecimal entryPrice;

    @Setter
    private BigDecimal exitPrice;

    @Setter
    private BigDecimal realizedPnl;

    /** 最大有利波动（美元）。 */
    @Setter
    private BigDecimal mfe;

    /** 最大不利波动（美元）。 */
    @Setter
    private BigDecimal mae;

    @Setter
    @Column(nullable = false)
    private int barsHeld;

    /** 成交笔数（开仓 + 加减仓 + 平仓）。 */
    @Setter
    @Column(nullable = false)
    private int fillCount;

    /**
     * 这一笔是否<strong>正常平完</strong>。
     *
     * <p>false 不是数据丢了：账户被删、程序退出、进程崩溃时仓位确实还在，
     * 那条记录就是「当时还没结束」。<strong>重启后继续用它</strong>——
     * 判据是「账户当前有没有未闭合记录」，有就接着录，没有才开新的。
     * 这样崩溃不会把一段交易劈成两条，也不需要额外的检查点。
     */
    @Setter
    @Column(nullable = false)
    private boolean closed;

    /**
     * 这些操作是谁做的。在<strong>开仓那一刻</strong>就定下来存好 —— 事后按 createdBy 猜不准：
     * 真人开的盘面被机器人接管之后开出来的那一笔，来源就是机器人。
     *
     * <p>取值只可能是 {@link Actor#HUMAN} 或 {@link Actor#BOT}，这一点由
     * {@link #open} 把关（{@code SYSTEM} 用来描述系统自动收尾，不是一笔交易的来源）。
     */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Actor actor;

    /** 给人看的细分：真人是操作者名字，机器人是策略名。改名不该让筛选分组裂开。 */
    @Setter
    private String actorName;

    @Setter
    private Instant endedAt;

    /** 用户打的标签，顺序即打标签的顺序。 */
    @Setter
    @ElementCollection(fetch = FetchType.EAGER)
    @CollectionTable(name = "trade_record_tag", joinColumns = @JoinColumn(name = "record_id"))
    @OrderColumn(name = "tag_index")
    private List<RecordTag> tags = new ArrayList<>();

    /**
     * 开一条记录。
     *
     * @param accountId   这笔交易算在哪个 PF 账户上，<strong>不能为空</strong>：没有账户就不该有交易
     * @param accountName 账户名快照，<strong>不能为空</strong>：账户被删之后，
     *                    这是唯一还记得「当初算在谁头上」的地方
     * @param symbolId    这笔交易做的是哪个品种，<strong>不能为空</strong>：回放要找到对应的时间轴
     * @param startSeq    开仓那一笔成交的 {@code trade.seq}，<strong>不能为负</strong>
     * @param actor       只能是 {@link Actor#HUMAN} 或 {@link Actor#BOT}
     * @throws IllegalArgumentException 没给账户 / 账户名 / 标的，seq 为负，或者传了 {@code SYSTEM}
     *                                  （它不是一笔交易的来源，而是「系统自己动的手」这类操作的操作者）
     */
    public static TradeRecord open(String accountId, String accountName, String symbolId,
                                   String anchorBarTime, int startSeq, Actor actor, String actorName) {
        if (actor != Actor.HUMAN && actor != Actor.BOT) {
            throw new IllegalArgumentException("交易记录的来源只能是 HUMAN 或 BOT，收到: " + actor);
        }
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("交易记录必须属于一个 PF 账户，收到: " + accountId);
        }
        if (accountName == null || accountName.isBlank()) {
            throw new IllegalArgumentException("交易记录必须记下账户名（快照位置），收到: " + accountName);
        }
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("交易记录必须指定标的，收到: " + symbolId);
        }
        if (startSeq < 0) {
            throw new IllegalArgumentException("起点事件的序号从 0 起，收到: " + startSeq);
        }
        TradeRecord record = new TradeRecord();
        record.accountId = accountId;
        record.accountName = accountName;
        record.symbolId = symbolId;
        record.anchorBarTime = anchorBarTime;
        record.startSeq = startSeq;
        record.actor = actor;
        record.actorName = actorName;
        return record;
    }

    /**
     * 把一条已经存在的记录挂到另一个账户上（典型场景：原来的账户被删了）。
     *
     * <p>只动归属和名字，不动内容 —— 记录里的每一步操作都是当时发生的事实，重挂不改历史。
     * 名字必须<strong>一起</strong>换：只换 id 不换名字，会得到一条「归属是新账户、
     * 显示着旧账户名」的记录，比两个都不换更难查。
     */
    public void attachTo(String accountId, String accountName) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("要挂到的账户不能为空，收到: " + accountId);
        }
        if (accountName == null || accountName.isBlank()) {
            throw new IllegalArgumentException("要挂到的账户必须有名字，收到: " + accountName);
        }
        this.accountId = accountId;
        this.accountName = accountName;
    }

    /** 这笔交易是从哪一边开的。还没有成交时无法确定。 */
    public void openPosition(Side side, String barTime, BigDecimal price, BigDecimal quantity) {
        this.side = side;
        this.entryBarTime = barTime;
        this.entryPrice = price;
        this.quantity = quantity;
        this.maxQuantity = quantity;
    }

    /**
     * 收尾：把仓位回到 0 那一刻的事实写进去，并把事件区间<strong>封口</strong>。
     *
     * <p>{@code endSeq} 是「平仓那一步」的 {@code trade.seq}。它必须
     * &ge; {@link #startSeq} —— 否则这段区间是反的，回放会读到空。
     * 反手时它<strong>等于</strong>下一条记录的 {@code startSeq}（同一笔成交的两条腿）。
     *
     * <p>区间和摘要在同一个方法里写，不拆成两步：拆开就会出现「已经收了尾但区间还开着」
     * 或者相反的中间态，而这两个字段描述的是<strong>同一件事</strong>。
     */
    public void closeAt(String barTime, BigDecimal price, BigDecimal realizedPnl, int endSeq) {
        if (endSeq < startSeq) {
            throw new IllegalArgumentException("终点序号不能小于起点（起点 " + startSeq
                    + "），收到: " + endSeq);
        }
        this.exitBarTime = barTime;
        this.exitPrice = price;
        // 摘要里的盈亏是 Σ 逐笔 realizedPnl 来的，逐笔已是「分」；这里再收敛一次是给
        // 「以后有人从别的路径往 closeAt 塞数」留的闸 —— 记录是统计视图，但它一样会落库。
        this.realizedPnl = Money.round(realizedPnl);
        this.endSeq = endSeq;
        this.closed = true;
        this.endedAt = Instant.now();
    }

    /**
     * 事件区间是否已经封口（收了尾、且 {@code end_seq} 有值）。
     *
     * <p>和 {@link #closed} 不等价：{@code closed} 是给列表筛选用的布尔，
     * 这个是给「能不能拿这段区间去回放」用的判据。两条判据都留着，是因为
     * 前者要能单独改（历史数据修正），后者不该跟着动。
     */
    public boolean hasSealedRange() {
        return closed && endSeq >= startSeq;
    }
}
