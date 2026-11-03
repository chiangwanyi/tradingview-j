package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.storage.Bar;

import java.math.BigDecimal;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

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
 * <h2>为什么「同一根里的成交」不能一视同仁</h2>
 *
 * <p>两种成交在<strong>一根 K 线之内</strong>的位置不一样：
 *
 * <ul>
 *   <li><strong>挂单（stop / limit）</strong>：盘中价格穿过挂单价就成交，之后价格还可能继续走坏
 *       —— 于是它<strong>要承担这一根的最不利价</strong>。这是保守的一侧，保留。</li>
 *   <li><strong>市价单</strong>：成交价就是<strong>这一根的收盘价</strong>（{@code TradingService#market}），
 *       而这一根的最低 / 最高发生在收盘<strong>之前</strong>。那一段波动它根本没赶上。</li>
 * </ul>
 *
 * <p>早期版本只看「成交时间 ≤ 这根」，于是市价单被拿去和同一根的极值配对，凭空多出一段浮亏。
 * 2026-11-03 在账户「7」上就是这样误判失败的：09:30 那根 low 7766、收盘 7770.50，
 * 收盘才买的 15 手被按 7766 标成浮亏 −1050，权益 47911.25 撞上 48000（只差 88.75），
 * 而当时的读数是 48256.25、状态 ACTIVE。
 *
 * <p>所以 {@link #equity} 的做法是「把本根收盘才成交的那几笔退回去」：用<strong>成交之前的持仓</strong>
 * 去挨这一根的极值，同时把余额里那几笔的已实现盈亏也退回去 —— 两边一起退，才不会重复计亏
 * （详见 {@link #equity} 的注释，那里记着 2026-11-03 账户「夜色粽子」的第二次误判）。
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
    /** 以<strong>当根收盘价</strong>成交的那些成交 id（市价单），见 {@link #equity}。 */
    private final Set<String> closeFilledTradeIds;

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
     * @param closeFilledTradeIds 以<strong>成交那一根的收盘价</strong>成交的成交 id（市价单）。
     *                    这些成交在 {@link #equity} 里不承担同根极值 —— 它们的成交价就是收盘价，
     *                    那一根的 low/high 发生在它们之前。挂单成交<strong>不要</strong>放进来。
     *                    判据只有一条：{@code trade_order.type == MARKET}
     */
    public PropFirmSession(PropFirmStage stage, PropFirmRules rules, BigDecimal baseBalance,
                           Map<String, Symbol> symbols, List<Trade> trades,
                           Set<String> closeFilledTradeIds) {
        this.stage = stage;
        this.rules = rules;
        this.baseBalance = baseBalance;
        this.balance = baseBalance;
        this.eodPeak = baseBalance;
        this.pointValues = new HashMap<>();
        this.tradesBySymbol = new LinkedHashMap<>();
        this.closeFilledTradeIds = closeFilledTradeIds == null ? Set.of() : Set.copyOf(closeFilledTradeIds);

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
            // 会话已经终态，但盘面还在往后走（考挂之后照样能一页页翻历史），
            // **成交仍然要记账**：撞线之后才有成交是常态 —— 收摊那笔平仓就落在「判定那根之后的
            // 下一根」上。这里若直接返回，那笔成交进了库却永远进不了余额，
            // 于是账上出现「已经失败、余额却还在亏损线之上」的自相矛盾。
            //
            // 只补余额 / 当日净额，不重判：终态是终态，权益再高也不复活。
            applyTradesUpTo(bar.barTime());
            return terminal(bar.barTime());
        }

        LocalDate day = PropFirmClock.tradingDayOf(bar.barTime());
        if (day == null) {
            // 04:00–06:00：不允许交易，既不属于前一天也不属于后一天。
            //
            // ⚠️ 这里**必须记账**：日终强平那笔平仓就记在 04:00 这一根上（用 04:00 的收盘价，
            // 见 TradeDeskService#step）。如果像以前那样直接 return，那笔成交永远进不了余额，
            // 紧接着 settleIfDayOver 的日终结算就会结在「平仓之前」的数上 ——
            // 账面上等于这次强平没发生过。SKIPPED 只表示「这根没有新行情可交易」，不表示「这根没有账」。
            applyTradesUpTo(bar.barTime());
            lastStep = new Step(Event.SKIPPED, bar.barTime(), null, balance, equity(bar.barTime()), floor(),
                    dayNet, maxDayNet, effectiveTarget(), profitDays, "禁止交易时段，跳过");
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

        BigDecimal equity = equity(bar.barTime());
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
     *
     * <p>FUNDED 若有 {@code profitTarget}（LucidFlex 50K 是 4000），和盈利日<strong>两个都要满足</strong>；
     * 没配目标的 FUNDED 仍只看盈利日。
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

        BigDecimal target = effectiveTarget();
        if (target != null && total.compareTo(target) < 0) {
            return "还差 " + plain(target.subtract(total)) + "（目标 " + plain(target) + "）";
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
     * <h2>「这根收盘才成交的市价单」要单独处理</h2>
     *
     * <p>它的成交价本来就是这根 K 线的收盘价，low/high 发生在它之前 —— 那一波它没赶上，
     * 不能拿极值去标它。做法是先把这些成交「退回去」，看这一根最难受的那一刻账上到底是
     * 什么状态：
     *
     * <pre>
     * 权益 = (余额 − 本根收盘成交那几笔的已实现盈亏) + 那些成交之前的持仓按极值标的浮盈亏
     * </pre>
     *
     * <p>两个细节，都是 2026-11-03 在账户「夜色粽子」上踩出来的：
     *
     * <ul>
     *   <li><strong>余额要先退回去</strong>：市价单平仓的已实现盈亏<strong>已经进了余额</strong>，
     *       若再拿「平仓前的持仓」按极值标一遍，同一段亏损就记了两次。
     *       旧实现在「平仓后立刻同向重开」时正是这样：16:40 平掉 5 手空单、又在同价开 5 手空单，
     *       持仓符号没变，`held` 判断不出这是「换了一批仓」，于是把已经平掉的那批也按极值标了，
     *       权益算出 47876，而真实盘中最低是 48291 —— 差 415 就是那笔重复计亏。</li>
     *   <li><strong>被平掉的那段仍要挨极值</strong>：它整根都在手上，这一根的最难受价它确实经历过，
     *       只是标价时要用「平仓前」的余额。旧实现在「整段仓位都在本根平掉」时直接跳过，
     *       等于把这一根的浮亏一笔勾销（偏松）。</li>
     * </ul>
     *
     * <p>于是不必再判断「本根是加仓、减仓还是反手」—— 一律用「成交前的持仓 + 成交前的余额」，
     * 只有一种口径。绝大多数 bar 没有收盘成交，`closeFillsAt` 返回空，走的就是原路径。
     *
     * <p>某个品种还没有 K 线时，它的浮盈亏按 0 算（刚开仓、盘面还没推进到它）。
     */
    private BigDecimal equity(String barTime) {
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
            BigDecimal pv = pointValue(symbolId);
            List<Trade> applied = entry.getValue().subList(0, cursor);

            List<Trade> atClose = closeFillsAt(applied, barTime);
            List<Trade> beforeClose = applied;
            if (!atClose.isEmpty()) {
                // 这笔钱在余额里，但此刻还没赚到（它是在收盘那一瞬才实现的）
                BigDecimal realized = ZERO;
                for (Trade trade : atClose) {
                    realized = realized.add(trade.getRealizedPnl());
                }
                total = total.subtract(realized);
                Set<String> ids = new java.util.HashSet<>();
                for (Trade trade : atClose) {
                    ids.add(trade.getId());
                }
                beforeClose = applied.stream().filter(t -> !ids.contains(t.getId())).toList();
            }

            Position held = TradeLedger.replay(beforeClose, pv).position();
            if (held.isFlat()) {
                continue;
            }
            // 多单怕 low、空单怕 high —— 用「这根 K 线里最难受的那个价」，
            // 剩下的符号处理交给 Position#unrealizedPnl（数量带符号，天然正确）
            BigDecimal worstPrice = held.quantity().signum() > 0 ? bar.low() : bar.high();
            total = total.add(held.unrealizedPnl(worstPrice, pv));
        }
        return total;
    }

    /** 这批成交里，落在 {@code barTime} 这根、且是「按收盘价成交」的那些。 */
    private List<Trade> closeFillsAt(List<Trade> applied, String barTime) {
        if (closeFilledTradeIds.isEmpty()) {
            return List.of();
        }
        List<Trade> out = new ArrayList<>(1);
        for (Trade trade : applied) {
            if (isCloseFillAt(trade, barTime)) {
                out.add(trade);
            }
        }
        return out;
    }

    private boolean isCloseFillAt(Trade trade, String barTime) {
        return closeFilledTradeIds.contains(trade.getId()) && barTime.equals(trade.getBarTime());
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
     * 一致性抬高之后的真实通关目标 —— <strong>公式在 {@link PropFirmRules#effectiveProfitTarget}
     * 一处</strong>，这里只是把会话里的「单日最大净盈利」喂进去。
     *
     * <p>例：单日赚 1800、比例 50% → 3600，比模板的 3000 高，所以目标被抬到 3600。
     *
     * @return {@code null} = 这套规则没有盈利目标（FUNDED）
     */
    private BigDecimal effectiveTarget() {
        return rules.effectiveProfitTarget(maxDayNet);
    }

    private Step terminal(String barTime) {
        return new Step(state, barTime, currentDay, balance, equity(barTime), floor(),
                dayNet, maxDayNet, effectiveTarget(), profitDays, "会话已结束");
    }

    private static String plain(BigDecimal value) {
        return value == null ? "null" : value.stripTrailingZeros().toPlainString();
    }

    // ---------------------------------------------------------------- 对外读数

    public Event state() {
        return state;
    }

    /**
     * 会话现在算到了<strong>哪个交易日</strong>；还没喂过任何 K 线时为 {@code null}。
     *
     * <p>只给外面做一件事：判断「这一天是不是已经走完了」——
     * {@code PropFirmJudge} 拿它和「下一根属于哪一天」比，决定要不要补一次日终结算。
     *
     * <p>注意它<strong>不因禁交易窗口的 K 线而前进</strong>（那些根走 {@code SKIPPED} 分支，
     * {@code currentDay} 原样不动），这正是需要的语义：04:00–06:00 是前一天的收尾，
     * 不属于新的一天。
     */
    public LocalDate tradingDay() {
        return currentDay;
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
