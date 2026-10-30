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
 * <p><strong>一切操作都必须发生在一个 PF 账户里</strong>（图形、挂单、成交、语音、操作记录、
 * 机器人、交易记录全部按 {@code accountId} 归属），但账户<strong>不拥有</strong>这些操作：
 * 删除账户只删这一行，子数据一条都不会被级联删除，它们的 {@code accountId} 原样保留。
 *
 * <p><strong>它是一个「临时的统计点」，不是一个「容器」</strong>。
 *
 * <h2>它绑定一个标的，而且一建就锁死</h2>
 *
 * <p>2026-10-30 起，账户在创建时必须指定<strong>唯一一个</strong>交易标的
 * （{@link #symbolId}），此后任何操作都不能换。所以「这个账户做哪个品种」这个问题
 * 永远只有一个答案，交易页也不必再提供标的切换。
 *
 * <p>在此之前账户是<strong>不绑标的</strong>的（一个账户同时做 MES / MNQ / MGC，
 * 品种下沉到每一条操作上），那一列被删过一次。这次回来的语义**不同**：
 * 它不是「最近做过的那个品种」这种会被顺手改写的缓存，而是账户的身份 ——
 * 所以用 {@code updatable = false} 钉在 ORM 层，且没有任何 setter。
 *
 * <p>子表（成交、挂单、交易记录、图形、语音）身上的 {@code symbol_id} 仍然保留：
 * 它们记的是「这条事实发生在哪块盘面上」，是历史留痕，不因为账户锁定了标的而变得多余。
 *
 * <h2>规则是「一个阶段」的规则，且会换</h2>
 *
 * <p>{@link #rules} 是<strong>当前阶段</strong>的规则快照（开账户时从模板复制），
 * 不是指向模板的引用。理由见 {@link PropFirmRules}：模板改了，我签的那一份不能变。
 * 阶段升级时整份快照被换成下一套模板的副本，所以<b>一个账户在不同时间签的是两份不同规则</b>
 * —— 这正是 {@link PropFirmStageLog} 存在的理由。
 *
 * <h2>为什么 balance 不能从成交重算</h2>
 *
 * <p>EVAL 通过时余额被<strong>强制拨回账户规模</strong>（EVAL 的盈利不带进 FUNDED）。
 * 所以「余额 = Σ已实现净额」这个等式只在<strong>一个阶段内</strong>成立。
 * 为此每个阶段记一个 {@link #baseBalance}，余额读作
 * {@code baseBalance + 本阶段净额}。这也是本类里除了 {@link #eodPeakBalance}
 * 之外第二个<strong>必须持久化</strong>的派生量。
 *
 * <p>{@link #eodPeakBalance} 是「<strong>日终余额</strong>的历史最高」，跟踪型回撤全靠它。
 * 之所以存下来而不是每次重算：它只在日终更新，而盘中推进时当前这根 K 线的日终余额还不存在；
 * 重算需要"每到 04:45 记一次"的时间序列，比存一个数贵得多。
 *
 * <p>⚠️ 判定（失败 / 升级）不在本类：本类只提供状态位和<b>被权威实现调用</b>的迁移动作，
 * 口径在 {@code service.PropFirmSession}。
 */
@Getter
@Entity
@Table(name = "prop_firm_account")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmAccount extends BaseEntity {

    @Setter
    @Column(nullable = false)
    private String name;

    /**
     * 从哪套模板开的（只用于追溯）。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：模板被删掉之后这里仍然保留原来那个 id，
     * 不置空 —— 账户上真正生效的是 {@link #rules} 那份快照，这一列只是用来回答「当初从哪套开的」。
     *
     * <p>阶段升级时<strong>不</strong>改它：它记的是「建户那一刻用的模板」，
     * 后来换了几次规则看阶段流水。
     */
    @Column(nullable = false)
    private String templateId;

    /**
     * 这个账户唯一可交易的标的（{@code symbol} 表的 id，也就是标的代码）。
     *
     * <p><strong>建户时定下，永不改动</strong>：{@code updatable = false} 让 Hibernate
     * 的 UPDATE 语句里根本不含这一列，类上也没有 setter —— 想改它只能改这一行的 SQL。
     * 「换个标的」不是「改一个字段」，而是「重开一个账户」，理由和模板一样：
     * 换了标的，起点的 K 线、所有复盘坐标、成交的时间轴全都要跟着换，那不是改字段。
     *
     * <p>{@code REFERENCES symbol (id)}：这是一个真的引用（和 {@code account_id} 那种
     * 「删了也要留着」的软指针相反）—— 指向一个不存在的标的的账户，连一根 K 线都读不出来。
     * {@code SymbolSyncService} 从不删标的，所以这条外键不会因为同步而失效。
     */
    @Column(name = "symbol_id", nullable = false, updatable = false)
    private String symbolId;

    /** 当前阶段。升级时连同 {@link #rules} 和余额基准一起换。 */
    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private PropFirmStage stage;

    /** 当前阶段的规则快照。 */
    @Embedded
    private PropFirmRules rules;

    /** 本阶段的起点余额。升级时重置成账户规模。 */
    @Column(nullable = false)
    private BigDecimal baseBalance;

    /** 本阶段的「日终余额」历史最高。只增不减，升级时重置。 */
    @Column(nullable = false)
    private BigDecimal eodPeakBalance;

    /** 当前余额 = {@link #baseBalance} + 本阶段已实现净额（已扣手续费）。 */
    @Column(nullable = false)
    private BigDecimal balance;

    /** 进入当前阶段的时刻。统计「本阶段成交」的时间下界。 */
    @Column(nullable = false)
    private Instant stageStartedAt;

    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private Status status;

    @Column(nullable = false)
    private Instant startedAt;

    /**
     * 起点那根 K 线的明文时间，如 {@code 2024-03-04 09:00}。
     *
     * <p><strong>这是「模拟时间」的零点，不是建户时刻。</strong> {@link #startedAt} 是真实时钟
     * （account 这一行是什么时候写进库的），这里记的是<strong>行情时间轴上的哪一根</strong>。
     * 两者必须分开：补录、回看、换机器都不会改变「这笔考核从哪一根开始」，
     * 而 {@code startedAt} 每次重建库都会变。
     *
     * <p>它由 {@code service.PropFirmStartPicker} 在开账户时随机抽出，此后
     * <strong>永不改动</strong>：日历的「首月」、可交易日的下界、回放的起点全部由它推出来。
     * 改一次，这个账户的所有历史都跟着挪位置。
     */
    @Column(nullable = false, updatable = false)
    private String startBarTime;

    /**
     * 起点所在的日文件名（{@code 2024-03-04.csv}，不含目录）。
     *
     * <p>按 {@code startBarTime} 前 10 位拼也拼得出来，留着它是一个<strong>名字</strong>
     * 而不是一个位置：文件名不会因为文件内容变了而指向别的一天，所以它不会撒谎。
     * 存下来是为了让「当时读的是哪一份」这件事不必靠字符串切片回答。
     */
    @Column(nullable = false, updatable = false)
    private String startDayFile;

    /**
     * 按模板开一个账户。规则在这一刻<strong>复制</strong>进来，此后与模板无关。
     *
     * <p>阶段由模板决定：拿 FUNDED 模板建出来的账户直接从 FUNDED 开始
     * （「有的是创建的时候就是 FUNDED」就是这条路径），流水里只会有 CREATED 一行。
     *
     * <p><strong>起点也是这一刻定的</strong>，而且必须有值：账户建出来就能开始看盘，
     * 没有「建好了但还没开始」这个状态。所以起点字段在库里是 {@code NOT NULL}
     * —— 允许 null 的话，每个读起点的地方都要处理一个谁也到不了的空值。
     * （2026-10-29 用户确认：起始日期在创建账户后由系统随机分配，定位到当天 09:00。）
     *
     * <p><strong>起点只有一个键：K 线时间。</strong> 曾经还有一个「在那份日文件里的下标」，
     * 已删除（2026-10-29）—— 位置会被内容悄悄改变（文件重新抓取、补一根、删一根坏行，
     * 同一个下标就指向另一根 K 线，而且不报错）。账户现在虽然绑定了标的，这条规则照样成立：
     * 同一份文件被重抓一次，下标就换了，时间不会撒这个谎。
     *
     * @param template 规则来源（它的 {@code stage} 就是新账户的初始阶段）
     * @param symbolId 唯一可交易的标的，<strong>不能为空</strong>；此后不可改
     * @param name     账户名（用户自己起，例如「LucidFlex 50K 第一次」）
     * @param startBarTime 系统抽中的起点 K 线明文，<strong>不能为空</strong>
     * @param startDayFile 起点所在的日文件名，<strong>不能为空</strong>
     */
    public static PropFirmAccount open(PropFirmTemplate template, String symbolId, String name,
                                       String startBarTime, String startDayFile) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("账户必须有名字，收到: " + name);
        }
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("账户必须指定交易标的，收到: " + symbolId);
        }
        if (template == null || template.getRules() == null) {
            throw new IllegalArgumentException("账户必须有模板和规则，收到: " + template);
        }
        if (startBarTime == null || startBarTime.isBlank()) {
            throw new IllegalArgumentException("账户必须有起点 K 线时间，收到: " + startBarTime);
        }
        if (startDayFile == null || startDayFile.isBlank()) {
            throw new IllegalArgumentException("账户必须记着起点在哪份日文件里，收到: " + startDayFile);
        }
        Instant now = Instant.now();
        PropFirmAccount account = new PropFirmAccount();
        account.name = name;
        account.templateId = template.getId();
        account.symbolId = symbolId;
        account.stage = template.getStage();
        account.rules = template.getRules().copy();
        account.baseBalance = template.getRules().getAccountSize();
        account.eodPeakBalance = account.baseBalance;
        account.balance = account.baseBalance;
        account.stageStartedAt = now;
        account.status = Status.ACTIVE;
        account.startedAt = now;
        account.startBarTime = startBarTime;
        account.startDayFile = startDayFile;
        return account;
    }

    /**
     * 改账户名。改名<strong>不是</strong>对盘面的操作，所以失败（{@code FAILED}）的账户也能改 ——
     * 它只是一个标签，不影响余额、回撤和阶段流水。
     *
     * @throws IllegalArgumentException 名字为空
     */
    public void rename(String name) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("账户必须有名字，收到: " + name);
        }
        this.name = name;
    }

    /** 还能不能往上挂操作（下单、画图、录音、起机器人）。 */
    public boolean isOpen() {
        return status == Status.ACTIVE;
    }

    /**
     * 这个账户能不能交易这个标的。
     *
     * <p>账户只做一个品种（建户时选定），别处的 {@code symbolId} 必须和它相等。
     * 拿这个判断而不是比较字段，是为了让「以后要不要放宽成多标的」只有一处要改。
     */
    public boolean tradesSymbol(String symbolId) {
        return this.symbolId != null && this.symbolId.equals(symbolId);
    }

    /** 还能不能下单。比 {@link #isOpen()} 多一条：PAYOUT 阶段虽然"开着"，但它是终点。 */
    public boolean isTradable() {
        return isOpen() && stage.tradable();
    }

    /** 本阶段赚了多少（已扣手续费）。可正可负。 */
    public BigDecimal netProfitInStage() {
        return balance.subtract(baseBalance);
    }

    /**
     * 把权威实现（{@code service.PropFirmSession}）算出来的余额和峰值同步过来。
     *
     * <p>这两个列是<strong>缓存</strong>：真值在「成交流水 + 规则快照」里，
     * 任何时候都可以从阶段起点重算一遍。写成缓存而不是每次现算，是因为界面上
     * 「还剩多少回撤」要显示在每一根 K 线上，每根都扫一遍成交不划算。
     *
     * <p><strong>这是修改余额的唯一入口，而且只用来「追平一个已经算好的结果」。</strong>
     * 曾经有一个 {@code applyNetPnl(netPnl)} 做增量累加，已经删掉 —— 它和这里
     * 各算一遍，两条路必然分叉，而且增量那条很容易漏掉开仓那一笔的手续费
     * （开仓的已实现盈亏是 0、净额是 {@code -commission}，只在平仓时累加就只扣了一半）。
     *
     * <p>想让余额变，正确做法是产生成交，然后重算并调这个方法。
     */
    public void syncComputed(BigDecimal balance, BigDecimal eodPeak) {
        if (balance != null) {
            this.balance = balance;
        }
        if (eodPeak != null) {
            this.eodPeakBalance = eodPeak;
        }
    }

    /**
     * 日终结算：用当前余额更新峰值。{@link PropFirmSession} 在跨交易日时调。
     *
     * <p>只增不减 —— 这也是"跟踪"两个字的全部含义。
     */
    public void settleEndOfDay() {
        if (balance.compareTo(eodPeakBalance) > 0) {
            this.eodPeakBalance = balance;
        }
    }

    /**
     * 升级到下一个阶段：换规则、换余额基准、换统计起点。
     *
     * <p>余额<strong>不是</strong>"接着上一阶段往下算"，而是重置成新模板的账户规模 ——
     * EVAL 赚的钱不带进 FUNDED。
     *
     * <p>{@code status} <strong>不动</strong>：进入 {@link PropFirmStage#PAYOUT} 之后
     * {@link #isTradable()} 会变成 {@code false}（不再接受下单），但账户本身仍然是
     * {@link Status#ACTIVE} —— 用户还可能在收工后回来标注、录音、看复盘，
     * 「走完流程」和「这行数据作废」是两件事。
     *
     * @param next      下一阶段的模板；{@code null} 表示升到尽头（进 {@link PropFirmStage#PAYOUT}）
     * @param nextStage 目标阶段
     */
    public void enterStage(PropFirmStage nextStage, PropFirmTemplate next, Instant at) {
        if (nextStage == null) {
            throw new IllegalArgumentException("升级目标阶段不能为空");
        }
        this.stage = nextStage;
        if (next != null && next.getRules() != null) {
            this.templateId = next.getId();
            this.rules = next.getRules().copy();
            this.baseBalance = next.getRules().getAccountSize();
            this.eodPeakBalance = this.baseBalance;
            this.balance = this.baseBalance;
        }
        this.stageStartedAt = at;
    }

    /** 判失败：撞了亏损线。之后不再接受任何操作。 */
    public void markFailed() {
        this.status = Status.FAILED;
    }

    public enum Status {
        /** 考核进行中（EVAL / FUNDED / PAYOUT 三个阶段都算）。 */
        ACTIVE,
        /**
         * 撞了亏损线，失败。终态。
         *
         * <p>只有"失败"才有一个专门的状态位；「走到 PAYOUT 了」由 {@link #stage} 表达
         * —— 再补一个 {@code COMPLETED} 就是同一个事实的第二份说法，
         * 两份说法迟早会有一天不一致。
         */
        FAILED
    }
}
