package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.storage.Bar;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * 逐根推进的考核判定器。用户说的「创建临时变量计算」就是这个类。
 *
 * <h2>它算三件事，而且只算三件事</h2>
 *
 * <ol>
 *   <li><strong>爆仓</strong>：每根 K 线收盘时，权益 ≤ 亏损线 → 失败。
 *       权益 = 余额 + 所有未平仓位的<strong>最不利浮盈亏</strong>（多单用 low、空单用 high）。</li>
 *   <li><strong>日终结算</strong>：跨交易日时，用当日收盘余额更新 EOD 峰值，亏损线随之（可能）上移。</li>
 *   <li><strong>升级</strong>：EVAL 看「总净盈利 ≥ 一致性抬高后的目标」，FUNDED 看「攒够几个盈利日」。</li>
 * </ol>
 *
 * <h2>为什么余额逐笔更新、亏损线只在日终更新</h2>
 *
 * <p>这是两套时钟，混成一个就全错：
 *
 * <ul>
 *   <li><strong>余额</strong>在<strong>每一笔成交</strong>时变（买进卖出都扣手续费，平仓实现盈亏）。
 *       等日终再算，「今天还剩多少回撤」这个问题在盘中就答不出来。</li>
 *   <li><strong>亏损线</strong>只在<strong>日终</strong>变。盘中赚到新高也不算 ——
 *       这就是「EOD 追踪」和「盘中追踪」的全部区别。</li>
 * </ul>
 *
 * <p>所以盘中判定用的是<strong>昨天收盘定下的那条线</strong>。盘中先冲高再回吐，
 * 只要不破昨天的线就还在场上；破的是浮亏，不是浮盈。
 *
 * <h2>为什么每根 K 线都重放一遍账本</h2>
 *
 * <p>持仓和均价一律走 {@link TradeLedger#replay}，<strong>不在这里维护第二套增量算法</strong>。
 * 两个实现迟早会在「反手」「部分平仓」这类边界上分叉，而分叉之后没有任何办法判断谁对。
 * 代价是 O(K 线数 × 成交数)：一个 20 天的阶段约 5500 根 × 几百笔，量级在百万次简单运算，
 * 毫秒级 —— 正确性比这点开销值钱。
 *
 * <h2>手续费</h2>
 *
 * <p>每一笔成交（开仓和平仓）都在发生时扣掉 {@link Trade#getCommission()}。
 * 所以「开仓那一根」的浮盈里已经少了进场费 —— 这就是真实账户的样子。
 *
 * <p>本类<strong>不写库、不判"账户能不能下单"</strong>，只回答"推到这一根时发生了什么"。
 * 写库和升级动作在 {@code PropFirmAccountService}。
 */
public final class PropFirmSession {

    private static final BigDecimal ZERO = BigDecimal.ZERO;

    private final PropFirmStage stage;
    private final PropFirmRules rules;
    private final BigDecimal baseBalance;
    private final Map<String, BigDecimal> pointValues;
    private final Map<String, List<Trade>> tradesBySymbol;

    /** 每个品种已经"应用过"的成交下标（只增不减，保证每笔成交只扣一次）。 */
    private final Map<String, Integer> cursors = new HashMap<>();
    /** 每个品种最新一根已知 K 线 —— 算浮亏用的极值从这里来。 */
    private final Map<String, Bar> lastBars = new HashMap<>();

    private BigDecimal balance;
    private BigDecimal eodPeak;
    private LocalDate currentDay;
    private BigDecimal dayNet = ZERO;
    private BigDecimal maxDayNet = ZERO;
    private int profitDays;
    private Event state = Event.RUNNING;
    private Step lastStep;

    /**
     * 从阶段起点开始重建一个会话。
     *
     * <p>余额从 {@code baseBalance} 起算、把该账户的成交全部重放一遍 ——
     * 库里那几个余额列是这个重算结果的<strong>缓存</strong>，不是第二份真相。
     *
     * @param stage       当前阶段（决定升级条件）
     * @param rules       当前阶段的规则快照
     * @param baseBalance 本阶段起点余额（升级时被重置成账户规模）
     * @param symbols     用到的标的（取 tick/tickValue 算每点价值）；缺一个就把那个品种的
     *                    浮盈亏按"每点 1 美元"退化，界面上要提示
     * @param trades      该账户的全部成交（内部按时间排序，顺序传错不影响结果）
     */
    public PropFirmSession(PropFirmStage stage, PropFirmRules rules, BigDecimal baseBalance,
                           Map<String, Symbol> symbols, List<Trade> trades) {
        this.stage = stage;
        this.rules = rules;
        this.baseBalance = baseBalance;
        this.balance = baseBalance;
        this.eodPeak = baseBalance;
        this.pointValues = new HashMap<>();
        this.tradesBySymbol = new LinkedHashMap<>();

        if (symbols != null) {
            symbols.forEach((id, symbol) -> pointValues.put(id,
                    TradeLedger.pointValue(symbol.getTick(), symbol.getTickValue())));
        }

        List<Trade> sorted = new ArrayList<>(trades == null ? List.of() : trades);
        sorted.sort(Trade.executionOrder());
        for (Trade trade : sorted) {
            tradesBySymbol.computeIfAbsent(trade.getSymbolId(), k -> new ArrayList<>()).add(trade);
        }
        for (String symbolId : tradesBySymbol.keySet()) {
            cursors.put(symbolId, 0);
        }
    }

    /**
     * 把盘面推进一根 K 线。
     *
     * <p>调用顺序必须是时间升序（可以多个品种交错）。同一时刻谁先谁后不影响结果：
     * 每根 K 线进来时，先把<strong>所有品种</strong>在该时刻及之前的成交应用掉，再判权益。
     *
     * @param symbolId 这根 K 线是哪个品种的
     */
    public Step onBar(String symbolId, Bar bar) {
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("K 线必须属于某个标的");
        }
        if (bar == null) {
            throw new IllegalArgumentException("K 线不能为空");
        }
        if (state != Event.RUNNING) {
            return terminal(bar.barTime());
        }

        LocalDate day = PropFirmClock.tradingDayOf(bar.barTime());
        if (day == null) {
            // 04:45–06:00：不允许交易，既不属于前一天也不属于后一天。K 线本身也不该存在。
            lastStep = new Step(Event.SKIPPED, bar.barTime(), null, balance, balance, floor(),
                    dayNet, maxDayNet, effectiveTarget(), profitDays, "禁交易窗口，跳过");
            return lastStep;
        }

        if (currentDay == null) {
            currentDay = day;
        } else if (day.isBefore(currentDay)) {
            throw new IllegalArgumentException(
                    "K 线必须按时间推进：" + bar.barTime() + " 早于当前交易日 " + currentDay);
        } else if (day.isAfter(currentDay)) {
            Step settled = settleDay();
            if (settled.event() != Event.RUNNING) {
                return settled;
            }
            currentDay = day;
        }

        applyTradesUpTo(bar.barTime());
        lastBars.put(symbolId, bar);

        BigDecimal equity = equity();
        BigDecimal line = floor();
        if (equity.compareTo(line) <= 0) {
            // 「等于」也算失败：用户明确要求（差 1 美分也已经没有缓冲）
            state = Event.FAILED;
            lastStep = new Step(Event.FAILED, bar.barTime(), day, balance, equity, line,
                    dayNet, maxDayNet, effectiveTarget(), profitDays,
                    "权益 " + plain(equity) + " 触及亏损线 " + plain(line));
            return lastStep;
        }

        lastStep = new Step(Event.RUNNING, bar.barTime(), day, balance, equity, line,
                dayNet, maxDayNet, effectiveTarget(), profitDays, null);
        return lastStep;
    }

    /**
     * 盘面结束（用户停在最后一根）。
     *
     * <p>必须调一次，否则<strong>最后一个交易日永远不结算</strong>：
     * 峰值不会更新、盈利日不会计数、EVAL 达标也不会升级。
     */
    public Step finish() {
        if (state != Event.RUNNING || currentDay == null) {
            return lastStep != null ? lastStep : new Step(Event.RUNNING, null, null,
                    balance, balance, floor(), dayNet, maxDayNet, effectiveTarget(), profitDays, null);
        }
        return settleDay();
    }

    // ---------------------------------------------------------------- 内部

    /** 日终结算。返回的 Step 若是 PROMOTED / FAILED，会话就结束了。 */
    private Step settleDay() {
        LocalDate settled = currentDay;
        String stamp = PropFirmClock.sessionEndOf(settled).toString().replace('T', ' ');

        // 1) 日终余额进峰值（只增不减）
        if (balance.compareTo(eodPeak) > 0) {
            eodPeak = balance;
        }
        // 2) 当日净额进"单日最大"（一致性拿它算，必须先于目标判定）
        if (dayNet.compareTo(maxDayNet) > 0) {
            maxDayNet = dayNet;
        }

        // 3) 盈利日（判据是"当日净额"，与累计无关）
        BigDecimal threshold = rules.getProfitDayThreshold();
        if (threshold != null && dayNet.compareTo(threshold) >= 0) {
            profitDays++;
        }

        // 4) 收盘余额已经在线下：前面每根都查过，这里兜一层（例如最后一根没有 K 线）
        BigDecimal line = floor();
        if (rules.getDrawdownType() == PropFirmRules.DrawdownType.TRAILING_EOD
                && balance.compareTo(line) <= 0) {
            state = Event.FAILED;
            return lastStep = new Step(Event.FAILED, stamp, settled, balance, balance, line,
                    dayNet, maxDayNet, effectiveTarget(), profitDays,
                    "日终余额 " + plain(balance) + " 触及亏损线 " + plain(line));
        }

        // 5) 升级判定
        String block = promotionBlockedBy();
        if (block == null) {
            state = Event.PROMOTED;
            return lastStep = new Step(Event.PROMOTED, stamp, settled, balance, balance, line,
                    dayNet, maxDayNet, effectiveTarget(), profitDays,
                    "达成 " + stage + " 的升级条件");
        }

        Step step = new Step(Event.RUNNING, stamp, settled, balance, balance, line,
                dayNet, maxDayNet, effectiveTarget(), profitDays, block);
        dayNet = ZERO;
        return lastStep = step;
    }

    /**
     * 升级条件还差什么。返回 {@code null} 表示可以升了。
     *
     * <p>EVAL 的目标里已经包含了「一致性抬高后的目标」，所以这里只需要比较一个数：
     * 一致性 50% 时，单日赚 1800 → 目标从 3000 抬到 3600，总盈利到 3600 才算过关。
     */
    private String promotionBlockedBy() {
        if (stage != PropFirmStage.EVAL && stage != PropFirmStage.FUNDED) {
            return "PAYOUT 是终点，不再升级";
        }
        BigDecimal total = balance.subtract(baseBalance);

        if (stage == PropFirmStage.EVAL) {
            BigDecimal target = effectiveTarget();
            if (target == null) {
                return "这套规则没有盈利目标，无法判定通过";
            }
            if (total.compareTo(target) < 0) {
                return "还差 " + plain(target.subtract(total)) + "（目标 " + plain(target) + "）";
            }
            if (total.signum() <= 0) {
                return "总净盈利必须为正";
            }
            return null;
        }

        Integer need = rules.getMinProfitDays();
        if (need == null || need <= 0) {
            return "这套规则没有盈利日要求，无法判定通过";
        }
        if (profitDays < need) {
            return "盈利日 " + profitDays + "/" + need;
        }
        return null;
    }

    /** 把所有品种在该时刻及之前的成交应用掉（每笔只应用一次）。 */
    private void applyTradesUpTo(String barTime) {
        for (Map.Entry<String, List<Trade>> entry : tradesBySymbol.entrySet()) {
            String symbolId = entry.getKey();
            List<Trade> list = entry.getValue();
            int cursor = cursors.getOrDefault(symbolId, 0);
            while (cursor < list.size() && list.get(cursor).getBarTime().compareTo(barTime) <= 0) {
                BigDecimal net = list.get(cursor).netPnl();
                balance = balance.add(net);
                dayNet = dayNet.add(net);
                cursor++;
            }
            cursors.put(symbolId, cursor);
        }
    }

    /**
     * 当前权益 = 余额 + 所有浮盈亏（最不利价）。
     *
     * <p>多单用这根 K 线的 {@code low}，空单用 {@code high}：用户要的就是
     * 「盘中持仓某根 K 线里最难受的那一刻有没有穿线」。用 {@code close} 会漏判
     * —— 一根下影线很长的 K 线可以浮亏穿线再收回来，收盘价看不出任何异常。
     *
     * <p>某个品种还没有 K 线时，它的浮盈亏按 0 算（刚开仓、盘面还没推进到它）。
     */
    private BigDecimal equity() {
        BigDecimal total = balance;
        for (Map.Entry<String, List<Trade>> entry : tradesBySymbol.entrySet()) {
            String symbolId = entry.getKey();
            int cursor = cursors.getOrDefault(symbolId, 0);
            if (cursor == 0) {
                continue;
            }
            Bar bar = lastBars.get(symbolId);
            if (bar == null) {
                continue;
            }
            Position position = TradeLedger.replay(
                    entry.getValue().subList(0, cursor), pointValue(symbolId)).position();
            BigDecimal qty = position.quantity();
            if (qty.signum() == 0) {
                continue;
            }
            // 多单怕 low、空单怕 high —— 用「这根 K 线里最难受的那个价」，
            // 剩下的符号处理交给 Position#unrealizedPnl（数量带符号，天然正确）
            BigDecimal worstPrice = qty.signum() > 0 ? bar.low() : bar.high();
            total = total.add(position.unrealizedPnl(worstPrice, pointValue(symbolId)));
        }
        return total;
    }

    private BigDecimal pointValue(String symbolId) {
        return pointValues.getOrDefault(symbolId, BigDecimal.ONE);
    }

    /**
     * 当前亏损线。
     *
     * <p>EOD 追踪 = 日终余额峰值 − 最大回撤，且峰值达到 {@code trailingLockLevel} 之后
     * <strong>冻结</strong>在 {@code lockLevel − 最大回撤}（LucidFlex 50K：52100 → 50100）。
     */
    private BigDecimal floor() {
        BigDecimal drawdown = rules.getMaxDrawdown();
        PropFirmRules.DrawdownType type = rules.getDrawdownType();
        if (type == null) {
            throw new IllegalStateException("规则没有指定回撤口径（drawdownType），拒绝猜一个");
        }
        switch (type) {
            case STATIC:
                return baseBalance.subtract(drawdown);
            case TRAILING_EOD: {
                BigDecimal lock = rules.getTrailingLockLevel();
                if (lock != null && eodPeak.compareTo(lock) >= 0) {
                    return lock.subtract(drawdown);
                }
                return eodPeak.subtract(drawdown);
            }
            case TRAILING_INTRADAY:
                // 没实现就不假装实现：算成 EOD 会让"盘中追踪"的考核无限宽松
                throw new IllegalStateException("TRAILING_INTRADAY 尚未实现，请用 TRAILING_EOD");
            default:
                throw new IllegalStateException("未知的回撤口径: " + type);
        }
    }

    /**
     * 一致性抬高之后的真实通关目标。
     *
     * <p>{@code 目标 = max(模板目标, 单日最大净盈利 / 一致性比例)}。
     * 例：单日赚 1800、比例 50% → 3600，比模板的 3000 高，所以目标被抬到 3600。
     *
     * <p>只要总盈利够大，这个比值自然降下来 —— 这正是「继续交易直到占比降回 50% 以下」
     * 那句话的算术表达，不需要一个"违规了"的中间状态。
     *
     * @return {@code null} = 这套规则没有盈利目标（FUNDED）
     */
    private BigDecimal effectiveTarget() {
        BigDecimal target = rules.getProfitTarget();
        BigDecimal ratio = rules.getConsistencyRatio();
        if (ratio == null || ratio.signum() <= 0) {
            return target;
        }
        if (maxDayNet.signum() <= 0) {
            return target;
        }
        BigDecimal need = maxDayNet.divide(ratio, 2, RoundingMode.HALF_UP);
        if (target == null) {
            return need;
        }
        return need.compareTo(target) > 0 ? need : target;
    }

    private Step terminal(String barTime) {
        return new Step(state, barTime, currentDay, balance, equity(), floor(),
                dayNet, maxDayNet, effectiveTarget(), profitDays, "会话已结束");
    }

    private static String plain(BigDecimal value) {
        return value == null ? "null" : value.stripTrailingZeros().toPlainString();
    }

    // ---------------------------------------------------------------- 对外读数

    public Event state() {
        return state;
    }

    public BigDecimal balance() {
        return balance;
    }

    public BigDecimal eodPeak() {
        return eodPeak;
    }

    public BigDecimal maxDayNet() {
        return maxDayNet;
    }

    public int profitDays() {
        return profitDays;
    }

    public BigDecimal floorLine() {
        return floor();
    }

    /** 推进结果。{@code event} 之外都是"推到这一刻"的读数，给界面直接用。 */
    public record Step(Event event, String barTime, LocalDate tradingDay,
                       BigDecimal balance, BigDecimal equity, BigDecimal floor,
                       BigDecimal dayNet, BigDecimal maxDayNet, BigDecimal effectiveTarget,
                       int profitDays, String message) {
    }

    public enum Event {
        /** 还在场上，什么都没发生。 */
        RUNNING,
        /** 这根 K 线落在禁交易窗口，没有推进。 */
        SKIPPED,
        /** 撞了亏损线。 */
        FAILED,
        /** 达标，该升级了 —— 会话到此结束，下一阶段要用新规则重开一个。 */
        PROMOTED
    }
}
