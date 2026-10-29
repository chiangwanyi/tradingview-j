package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Embedded;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.math.BigDecimal;
import java.time.Instant;

/**
 * Prop Firm 账户：一个<strong>具体在考核中的</strong>账户，也是全库唯一的<strong>操作归属点</strong>。
 *
 * <p><strong>它取代了原来的「复盘会话」（ReplaySession）</strong>。理由不是「两个东西长得像」，
 * 而是它们回答的是同一个问题：这次操作算在谁头上。留着会话就会出现「操作属于会话、账算在账户」
 * 两套归属，任何一个统计问题都要先想清楚走哪条线。现在只有一条：
 * <strong>一切操作都必须发生在一个 PF 账户里</strong>（图形、挂单、成交、语音、操作记录、
 * 机器人、交易记录全部按 {@code accountId} 归属）。
 *
 * <p><strong>它是一个「临时的统计点」，不是一个「容器」</strong>：统计考核期内的一切操作，
 * 但<strong>不拥有</strong>这些操作。删除账户只删这一行，下面的成交、交易记录、语音、
 * 操作记录、图形<strong>一条都不会被级联删除</strong>——它们与账户在数据归属上相互独立，
 * 只在「算在谁头上」这一点上引用账户。账户删掉之后，那些行的 {@code account_id} 被数据库
 * 置为 NULL（外键 {@code ON DELETE SET NULL}），数据本身原样保留，可以由用户手动删除，
 * 也可以将来重新挂到另一个账户上。
 *
 * <p>⚠️ 由此产生一个<strong>必须知道的边界</strong>：表上的 {@code account_id} 是可空的
 * （只能是「账户被删了」这一种情况造成）。「创建时必须属于某个账户」这条规则由
 * 领域方法（各实体的工厂方法 + {@code service.PropFirmAccountService#requireActive}）把关，
 * 不由列上的 {@code NOT NULL} 把关 —— 因为两者不能同时成立。
 *
 * <p>{@link #symbolId} 是<strong>账户开在哪个标的上</strong>。会话被移除之后，它是
 * 「这块盘面是哪个品种」的唯一落点：图形和成交只存 K 线时间与价格，没有标的就找不到
 * 对应的时间轴。{@code ReplaySession} 原来的锚点、游标、操作人<strong>不搬到这里</strong>，
 * 那是盘面推进过程中的运行期状态，不属于账户。
 *
 * <p>{@link #rules} 是<strong>开账户时从模板复制进来的副本</strong>，
 * 不是指向模板的引用。理由见 {@link PropFirmRules}：模板改了，我签的那一份不能变。
 * {@link #templateId} 只用来回答「这个账户是从哪套模板开的」。
 *
 * <p>{@link #peakEquity} 是「历史最高权益」，跟踪型回撤全靠它。之所以<strong>存下来</strong>
 * 而不是每次从成交重算：峰值是<strong>路径依赖</strong>的 —— 重算需要知道每一个时刻的
 * 浮盈浮亏，而成交记录里没有「每一刻的持仓市值」。这是本类里唯一必须持久化的派生量。
 *
 * <p>⚠️ 待确认：{@link #status} 什么时候从 ACTIVE 变成 PASSED / FAILED、
 * {@link #tradingDays} 怎么数（跨日算一天还是有过成交才算），口径未定，
 * 本类只提供状态位，不做判定。
 */
@Getter
@Entity
@Table(name = "prop_firm_account")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmAccount extends BaseEntity {

    @Setter
    private String name;

    /** 这个账户做的是哪个标的（外键指向 {@code symbol.id}），图形和成交靠它找到时间轴。 */
    @Column(nullable = false)
    private String symbolId;

    /** 从哪套模板开的（只用于追溯，模板删了就置 NULL）。 */
    @Column
    private String templateId;

    /** 规则快照。 */
    @Embedded
    private PropFirmRules rules;

    /** 当前余额（已结算的现金）。 */
    @Setter
    @Column(nullable = false)
    private BigDecimal balance;

    /** 历史最高权益。跟踪型回撤的基准，只增不减。 */
    @Setter
    @Column(nullable = false)
    private BigDecimal peakEquity;

    /** 已经过的交易日数。 */
    @Setter
    @Column(nullable = false)
    private int tradingDays;

    @Setter
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Status status;

    @Column(nullable = false)
    private Instant startedAt;

    /**
     * 按模板开一个账户。规则在这一刻<strong>复制</strong>进来，此后与模板无关。
     *
     * @param template 规则来源
     * @param name     账户名（用户自己起，例如「Apex 50K 第一次」）
     * @param symbolId 这个账户做哪个标的
     */
    public static PropFirmAccount open(PropFirmTemplate template, String name, String symbolId) {
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("账户必须绑定一个标的，收到: " + symbolId);
        }
        PropFirmAccount account = new PropFirmAccount();
        account.name = name;
        account.symbolId = symbolId;
        account.templateId = template.getId();
        account.rules = template.getRules().copy();
        account.balance = template.getRules().getAccountSize();
        account.peakEquity = account.balance;
        account.status = Status.ACTIVE;
        account.startedAt = Instant.now();
        return account;
    }

    /** 还能不能往上挂操作。删掉的账户（行已不存在）、失败的账户都不该继续下单。 */
    public boolean isOpen() {
        return status == Status.ACTIVE;
    }

    public enum Status {
        /** 考核进行中。 */
        ACTIVE,
        /** 达标通过。 */
        PASSED,
        /** 触发回撤/日内亏损，失败。 */
        FAILED
    }
}
