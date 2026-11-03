package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmSettlement;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.storage.Bar;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.time.LocalDate;
import java.util.List;
import java.util.Map;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 会话终态之后，<strong>成交还得记账</strong>。
 *
 * <p>这条是「账户显示失败、余额却还在亏损线之上」的根因（2026-11-01 在账户「4」上暴露）：
 *
 * <ul>
 *   <li>撞线是在<strong>某根 K 线</strong>上判出来的（用那根的最低 / 最高价算最不利浮亏）；</li>
 *   <li>收摊平仓是<strong>之后</strong>才发生的一笔成交，落在「判定那根之后的下一根」上；</li>
 *   <li>{@code onBar} 一旦发现会话已终态就直接返回，于是那笔成交进了库、却永远没进过余额。</li>
 * </ul>
 *
 * <p>症状很刺眼：{@code status = FAILED}，余额却高于亏损线，阶段流水还记成一笔<strong>盈利</strong>。
 */
class PropFirmSessionTest {

    private static final String CODE = "CME_MINI:MES1!";
    private static final String FOLDER = "CME_MINI_MES1!";

    /**
     * 06-11 19:15 那一幕的缩小版：破线那根（09:05）之后，收摊平仓落在下一根（09:10）。
     *
     * <p>基数 50000、回撤 2000 → 亏损线 48000。开多 1 手 @4000，手续费 1 → 余额 49999；
     * 09:05 那根最低 3600，最不利浮亏 −2000 → 权益 47999，破线。
     * 09:10 收摊平仓 @3600，实现 −400、手续费 1 → 余额应为 <strong>49598</strong>。
     *
     * <p>钉三件事：
     *
     * <ul>
     *   <li>平仓那笔<strong>进了余额</strong>（修复前停在 49999，少 401）；</li>
     *   <li>终态之后不再重判 —— 平仓后权益 49598 又回到 48000 之上，照样是 FAILED，
     *       不能因为「账面看起来缓过来了」就复活；</li>
     *   <li>再往后喂多少根，余额都不再变（幂等，不重复扣同一笔）。</li>
     * </ul>
     */
    @Test
    void tradesAfterTerminalStillCountTowardBalance() {
        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("1.25"));
        // 每点价值 = 1.25 / 0.25 = 5 美元

        Trade open = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.BUY,
                new BigDecimal("4000.00"), BigDecimal.ONE, BigDecimal.ZERO, new BigDecimal("1.00"));
        // 收摊平仓：判定那根（09:05）之后的下一根才成交，正是修复前被吞掉的那一笔
        Trade closeOut = Trade.fill("acc", CODE, "o2", "2024-03-04 09:10", 2, Side.SELL,
                new BigDecimal("3600.00"), BigDecimal.ONE, new BigDecimal("-400.00"), new BigDecimal("1.00"));

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol), List.of(open, closeOut), Set.of());

        session.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        PropFirmSession.Step breached =
                session.onBar(CODE, bar("2024-03-04 09:05", "4000.00", "4002.00", "3600.00", "3600.00"));
        assertEquals(PropFirmSession.Event.FAILED, breached.event(), "最不利浮亏 −2000 已经踩到亏损线 48000");

        PropFirmSession.Step afterClose =
                session.onBar(CODE, bar("2024-03-04 09:10", "3600.00", "3600.00", "3600.00", "3600.00"));
        assertEquals(PropFirmSession.Event.FAILED, afterClose.event(), "终态不重判：权益回到线上也不复活");
        assertMoney("49598", session.balance());
        assertMoney("49598", afterClose.balance());
        assertMoney("49598", afterClose.equity());

        // 再往后走：余额纹丝不动（同一笔不会被重复应用）
        session.onBar(CODE, bar("2024-03-04 09:15", "3600.00", "3600.00", "3600.00", "3600.00"));
        assertMoney("49598", session.balance());
    }

    /**
     * FUNDED 有目标时，目标与盈利日<strong>两个都要满足</strong>才能升 PAYOUT。
     *
     * <p>目标是 4000，5 天各赚 1000（盈利日 5/5、总盈利 5000）：目标 6000 的变体卡在「还差 1000」，
     * 目标 4000 的变体当天走完就 PROMOTED。合计 5000 差 1000 的那一步正是「只看盈利日」会放过去的地方。
     */
    @Test
    void fundedRequiresTargetAndProfitDays() {
        assertEquals(PropFirmSession.Event.RUNNING, driveFunded("6000").event(),
                "盈利日 5/5 但总盈利 5000 < 6000，不能过关");
        assertEquals(PropFirmSession.Event.PROMOTED, driveFunded("4000").event(),
                "目标 4000 与盈利日 5/5 都够了，应当升 PAYOUT");
    }

    /**
     * <strong>按当根收盘价成交的市价单，不承担这一根的极值。</strong>
     *
     * <p>2026-11-03 账户「7」的误判就是这条：09:30 那根 low 7766 / 收盘 7770.50，
     * 收盘才市价买入的 15 手被拿 7766 去标，凭空多出 −1050 浮亏，权益 47911.25 撞上 48000
     * （当时真实读数是 48256.25、状态 ACTIVE）。
     *
     * <p>这里把它放大到一眼能分辨：开多 15 手 @7775.50（挂单成交，挨这一根），
     * 同一根收盘再市价买 15 手 @7770.50。那根 low 7750。
     *
     * <ul>
     *   <li>只标「原来那 15 手」→ (7750 − 7775.50) × 5 × 15 = −1912.5，权益 <strong>48087.5</strong>；</li>
     *   <li>若把收盘才买的也算进去（平均成本 7773、30 手）→ −3450，权益 <strong>46550</strong>，撞线失败。</li>
     * </ul>
     */
    @Test
    void marketFillAtBarCloseDoesNotBiteTheSameBarExtreme() {
        Trade held = Trade.fill("acc", CODE, "o1", "2024-03-04 09:25", 1, Side.BUY,
                new BigDecimal("7775.50"), new BigDecimal("15"), BigDecimal.ZERO, BigDecimal.ZERO);
        Trade atClose = Trade.fill("acc", CODE, "o2", "2024-03-04 09:30", 2, Side.BUY,
                new BigDecimal("7770.50"), new BigDecimal("15"), BigDecimal.ZERO, BigDecimal.ZERO);

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol()), List.of(held, atClose),
                Set.of(atClose.getId()));

        session.onBar(CODE, bar("2024-03-04 09:25", "7775.50", "7775.50", "7775.50", "7775.50"));
        PropFirmSession.Step step =
                session.onBar(CODE, bar("2024-03-04 09:30", "7767.50", "7770.75", "7750.00", "7770.50"));

        assertEquals(PropFirmSession.Event.RUNNING, step.event(),
                "收盘才成交的 15 手没赶上那一根的低点，不能拿 7750 去标它");
        assertMoney("48087.50", step.equity());
        assertMoney("50000.00", step.balance());
    }

    /**
     * 同一根、同样价格，但那一笔是<strong>挂单</strong>成交 —— 仍然按极值标，该失败就失败。
     *
     * <p>挂单是盘中价格穿过才成交的，成交之后价格还可能继续走坏，所以保守一侧保留。
     * 这条和上一条成对：区别只有「这一笔是不是市价单」，结论却从活变死。
     */
    @Test
    void stopFillAtBarCloseStillBitesTheSameBarExtreme() {
        Trade held = Trade.fill("acc", CODE, "o1", "2024-03-04 09:25", 1, Side.BUY,
                new BigDecimal("7775.50"), new BigDecimal("15"), BigDecimal.ZERO, BigDecimal.ZERO);
        Trade atClose = Trade.fill("acc", CODE, "o2", "2024-03-04 09:30", 2, Side.BUY,
                new BigDecimal("7770.50"), new BigDecimal("15"), BigDecimal.ZERO, BigDecimal.ZERO);

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol()), List.of(held, atClose), Set.of());

        session.onBar(CODE, bar("2024-03-04 09:25", "7775.50", "7775.50", "7775.50", "7775.50"));
        PropFirmSession.Step step =
                session.onBar(CODE, bar("2024-03-04 09:30", "7767.50", "7770.75", "7750.00", "7770.50"));

        assertEquals(PropFirmSession.Event.FAILED, step.event(), "挂单成交要承担这一根的极值");
        assertMoney("46550.00", step.equity());
    }

    /**
     * 同一根里「平掉旧仓 + 同方向再开」：<strong>同一段亏损不能记两次</strong>。
     *
     * <p>2026-05-26 16:40 账户「夜色粽子」的缩小版：空 5 手 @30347 一直在手，收盘那一刻
     * 先市价买回 5 手平掉（实现 −410），又市价卖出 5 手重新开空。持仓符号没变（都是空 5 手），
     * 所以「剔掉收盘成交再重放」拿到的 {@code held} 还是那笔已经平掉的老仓 ——
     * 它的 −410 已经进了余额，若再拿它去挨 30392.25 的极值，等于同一段亏损记两遍。
     *
     * <p>正确读数：余额 49590、盘中最低权益 = 50000 − 45.25 × 5 × 5 = <strong>48868.75</strong>
     * （用「平仓前余额 50000 + 老仓按极值标的 −1131.25」）。
     * 旧算法给的是 48458.75（少 410）；id 错配那版给的是 49483.75（多算了新仓的极值）。
     */
    @Test
    void closeThenReopenSameSideDoesNotCountTheSameLossTwice() {
        Trade held = Trade.fill("acc", CODE, "o1", "2024-03-04 16:00", 1, Side.SELL,
                new BigDecimal("30347.00"), new BigDecimal("5"), BigDecimal.ZERO, BigDecimal.ZERO);
        Trade closed = Trade.fill("acc", CODE, "o2", "2024-03-04 16:40", 2, Side.BUY,
                new BigDecimal("30388.00"), new BigDecimal("5"), new BigDecimal("-410.00"), BigDecimal.ZERO);
        Trade reopened = Trade.fill("acc", CODE, "o3", "2024-03-04 16:40", 3, Side.SELL,
                new BigDecimal("30388.00"), new BigDecimal("5"), BigDecimal.ZERO, BigDecimal.ZERO);

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol()),
                List.of(held, closed, reopened), Set.of(closed.getId(), reopened.getId()));

        session.onBar(CODE, bar("2024-03-04 16:00", "30350.00", "30350.00", "30345.00", "30347.00"));
        PropFirmSession.Step step = session.onBar(CODE, bar("2024-03-04 16:40",
                "30381.75", "30392.25", "30376.75", "30388.00"));

        assertMoney("49590.00", step.balance());
        assertMoney("48868.75", step.equity());
        assertEquals(PropFirmSession.Event.RUNNING, step.event(), "48868.75 还在亏损线 48000 之上");
    }

    /**
     * 整段仓位在<strong>本根收盘</strong>平掉：这一根的最难受价它经历过，照样要挨极值。
     *
     * <p>旧实现在「平完就空仓」时直接跳过（{@code qty == 0 → continue}），等于把这一根的浮亏
     * 一笔勾销 —— 一根插针把仓位扫出局、收盘前又回头，账面就完全看不出发生过什么。
     *
     * <p>正确读数：余额 49750（实现 −250）+ 把 −250 退回 → 50000，再按最低 3900 标 5 手多单
     * = 50000 − 100 × 5 × 5 = <strong>47500</strong>，破线。
     */
    @Test
    void positionClosedAtBarCloseStillBitesThatBarsExtreme() {
        Trade open = Trade.fill("acc", CODE, "o1", "2024-03-04 09:00", 1, Side.BUY,
                new BigDecimal("4000.00"), new BigDecimal("5"), BigDecimal.ZERO, BigDecimal.ZERO);
        Trade close = Trade.fill("acc", CODE, "o2", "2024-03-04 09:05", 2, Side.SELL,
                new BigDecimal("3950.00"), new BigDecimal("5"), new BigDecimal("-250.00"), BigDecimal.ZERO);

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol()),
                List.of(open, close), Set.of(close.getId()));

        session.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        PropFirmSession.Step step = session.onBar(CODE, bar("2024-03-04 09:05",
                "4000.00", "4000.00", "3900.00", "3950.00"));

        assertMoney("49750.00", step.balance());
        assertMoney("47500.00", step.equity());
        assertEquals(PropFirmSession.Event.FAILED, step.event(), "插针 3900 已经破了 48000");
    }

    /** 5 个交易日、每天净赚 1000，走完最后一天并结账。 */
    private static PropFirmSession.Step driveFunded(String target) {        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("1.25"));

        PropFirmRules rules = new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal(target),
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100.00"), null, 5, new BigDecimal("150.00"),
                List.of(new PositionTier(BigDecimal.ZERO, 20)));

        String[] days = {"2024-03-04", "2024-03-05", "2024-03-06", "2024-03-07", "2024-03-08"};
        List<Trade> trades = new java.util.ArrayList<>();
        for (int i = 0; i < days.length; i++) {
            trades.add(Trade.fill("acc", CODE, "o" + (i + 1), days[i] + " 09:05", i + 1, Side.SELL,
                    new BigDecimal("4000.00"), BigDecimal.ONE, new BigDecimal("1000.00"), BigDecimal.ZERO));
        }

        PropFirmSession session = new PropFirmSession(PropFirmStage.FUNDED, rules,
                new BigDecimal("50000.00"), Map.of(CODE, symbol), trades, Set.of());
        for (String day : days) {
            session.onBar(CODE, bar(day + " 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
            session.onBar(CODE, bar(day + " 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        }
        return session.finish();
    }

    /** MES：tick 0.25 / tickValue 1.25 → 每点价值 5 美元。 */
    /**
     * 这一天结过账之后，收尾窗口里又来了一笔成交（最典型的是 04:00 那根上的日终强平）：
     * <strong>余额高了就把峰值抬上去，低了就什么都不做</strong>。
     *
     * <p>2026-11-03 的数据：6 户 × 300 根剩下 49 条 {@code FLOOR_DRIFT}，全是
     * 「后端 51159.06 / 重算 51166.77」这种几块钱的差 —— 差的就是 03:55 结账之后
     * 04:00 强平那一笔。峰值只增不减，所以只补「更高」的那一侧。
     */
    @Test
    void aLateFillInTheSameDayOnlyRaisesThePeak() {
        Symbol symbol = symbol();
        // 09:10 赚 48、扣两边手续费 → 余额 50046（这是第一次结算看到的数）；09:15 再赚 52 → 50097
        Trade open = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.BUY,
                new BigDecimal("4000.00"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ONE);
        Trade take = Trade.fill("acc", CODE, "o2", "2024-03-04 09:10", 2, Side.SELL,
                new BigDecimal("4009.60"), BigDecimal.ONE, new BigDecimal("48.00"), BigDecimal.ONE);
        Trade rest = Trade.fill("acc", CODE, "o3", "2024-03-04 09:15", 3, Side.BUY,
                new BigDecimal("4000.00"), BigDecimal.ONE, new BigDecimal("52.00"), BigDecimal.ONE);

        PropFirmSession.Settlement first = new PropFirmSession.Settlement(
                LocalDate.parse("2024-03-04"), new BigDecimal("50046.00"), new BigDecimal("50046.00"),
                BigDecimal.ZERO, 0, PropFirmSettlement.Outcome.RUNNING, "还差 2954（目标 3000）");

        // ① 收尾那笔是**赚**的：峰值被补到 50097，并产生一条待更新的记录
        PropFirmSession up = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol), List.of(open, take, rest), Set.of(),
                Map.of(first.tradingDay(), first));
        up.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        up.onBar(CODE, bar("2024-03-04 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        up.onBar(CODE, bar("2024-03-04 09:10", "4000.00", "4009.60", "4000.00", "4009.60"));
        up.onBar(CODE, bar("2024-03-04 09:15", "4009.60", "4009.60", "4000.00", "4000.00"));
        up.finish();
        assertMoney("50097.00", up.eodPeak());
        assertEquals(1, up.pendingSettlements().size(), "峰值涨了，要交回一条补正记录");
        assertMoney("50097.00", up.pendingSettlements().get(0).peakBalance());

        // ② 收尾那笔是**亏**的：峰值一动不动，也不产生任何待写记录
        Trade loss = Trade.fill("acc", CODE, "o4", "2024-03-04 09:15", 4, Side.BUY,
                new BigDecimal("4000.00"), BigDecimal.ONE, new BigDecimal("-52.00"), BigDecimal.ONE);
        PropFirmSession down = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol), List.of(open, take, loss), Set.of(),
                Map.of(first.tradingDay(), first));
        down.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        down.onBar(CODE, bar("2024-03-04 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        down.onBar(CODE, bar("2024-03-04 09:10", "4000.00", "4009.60", "4000.00", "4009.60"));
        down.onBar(CODE, bar("2024-03-04 09:15", "4000.00", "4000.00", "4000.00", "4000.00"));
        down.finish();
        assertMoney("50046.00", down.eodPeak());
        assertTrue(down.pendingSettlements().isEmpty(), "峰值没变，不该产生写库动作");
    }

    private static Symbol symbol() {
        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("1.25"));
        return symbol;
    }

    /**
     * 已经落过库的那一天，峰值<strong>不许被下一轮重放拉低</strong>。
     *
     * <p>这是 2026-11-03 机器人压测刷出 61 条 {@code FLOOR_DRIFT} 的根因：日终结算点跟着
     * 「用户推到哪一根」走 —— 同一份成交停在 09:10 结出 50497，再推进到 09:15
     * （那根又付了一笔手续费）就结出 50496，<strong>峰值回落 1</strong>，
     * 而亏损线 = 峰值 − 2000，线跟着回落。「只增不减」在跨调用这一层曾经完全失效。
     *
     * <p>现在结算是一次<strong>写入事实</strong>：第二轮把第一轮的快照传进去，那天就不再重算。
     */
    @Test
    void aSettledDayKeepsItsPeakAcrossPasses() {
        Symbol symbol = symbol();
        Trade open = Trade.fill("acc", CODE, "o1", "2024-03-04 09:05", 1, Side.BUY,
                new BigDecimal("4000.00"), new BigDecimal("2"), BigDecimal.ZERO, new BigDecimal("2.00"));
        Trade take = Trade.fill("acc", CODE, "o2", "2024-03-04 09:10", 2, Side.SELL,
                new BigDecimal("4100.00"), BigDecimal.ONE, new BigDecimal("500.00"), new BigDecimal("1.00"));
        Trade rest = Trade.fill("acc", CODE, "o3", "2024-03-04 09:15", 3, Side.SELL,
                new BigDecimal("4000.00"), BigDecimal.ONE, BigDecimal.ZERO, new BigDecimal("1.00"));
        List<Trade> flow = List.of(open, take, rest);
        Map<String, Symbol> symbols = Map.of(CODE, symbol);

        // 第一轮：只推到 09:10 → 余额 50000 − 2 + (500 − 1) = 50497
        PropFirmSession first = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), symbols, flow, Set.of());
        first.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        first.onBar(CODE, bar("2024-03-04 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        first.onBar(CODE, bar("2024-03-04 09:10", "4000.00", "4100.00", "4000.00", "4100.00"));
        first.finish();
        assertMoney("50497.00", first.balance());
        assertMoney("50497.00", first.eodPeak());
        assertEquals(1, first.pendingSettlements().size(), "这一天才第一次结算，应该等着落库");
        PropFirmSession.Settlement day = first.pendingSettlements().get(0);

        // 第二轮：同一天又多推了一根，最后一笔手续费把余额压到 50496
        PropFirmSession second = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), symbols, flow, Set.of(),
                Map.of(day.tradingDay(), day));
        second.onBar(CODE, bar("2024-03-04 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
        second.onBar(CODE, bar("2024-03-04 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        second.onBar(CODE, bar("2024-03-04 09:10", "4000.00", "4100.00", "4000.00", "4100.00"));
        second.onBar(CODE, bar("2024-03-04 09:15", "4100.00", "4100.00", "4000.00", "4000.00"));
        second.finish();

        assertMoney("50496.00", second.balance());
        assertMoney("50497.00", second.eodPeak());  // 修复前是 50496：峰值被「多推一根」拉低了
        assertTrue(second.pendingSettlements().isEmpty(), "这一天已经在库里，不该再产生待写记录");
    }

    /**
     * 已经判过失败的那一天，重放时<b>不能翻面</b>。
     *
     * <p>判决是历史，不是函数：如果每次都按当前余额重判，「达标那一刻」会随着后来的成交
     * 来回翻（收在目标之上、次日回撤到线下，重放就会先判没达标、再判失败，
     * 而当时真实发生的其实是「达标 → 冻结」）。这里钉住失败这一侧。
     */
    @Test
    void aSettledFailureStaysFailedEvenIfTheReplayLooksFine() {
        Symbol symbol = symbol();
        PropFirmSession.Settlement verdict = new PropFirmSession.Settlement(
                LocalDate.parse("2024-03-04"), new BigDecimal("50000.00"), new BigDecimal("50000.00"),
                BigDecimal.ZERO, 0, PropFirmSettlement.Outcome.FAILED,
                "日终余额 47900.00 触及亏损线 48000.00");

        PropFirmSession session = new PropFirmSession(PropFirmStage.EVAL, rules(),
                new BigDecimal("50000.00"), Map.of(CODE, symbol), List.of(), Set.of(),
                Map.of(verdict.tradingDay(), verdict));
        session.onBar(CODE, bar("2024-03-04 09:00", "50000.00", "50000.00", "50000.00", "50000.00"));

        PropFirmSession.Step step = session.finish();
        assertEquals(PropFirmSession.Event.FAILED, step.event(), "库里写着那天失败了，重放不能判成还在场上");
        assertEquals(PropFirmSession.Event.FAILED, session.state());
        assertMoney("48000.00", step.floor());
    }

    /** EVAL 50K：规模 50000、目标 3000、回撤 2000、冻结水位 52100、一致性 50%。 */
    private static PropFirmRules rules() {
        return new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                List.of(new PositionTier(BigDecimal.ZERO, 40)));
    }

    private static Bar bar(String time, String open, String high, String low, String close) {
        return new Bar(time, new BigDecimal(open), new BigDecimal(high), new BigDecimal(low),
                new BigDecimal(close), new BigDecimal("100"));
    }

    private static void assertMoney(String expected, BigDecimal actual) {
        assertEquals(0, new BigDecimal(expected).compareTo(actual),
                "期望 " + expected + "，实际 " + actual);
    }
}
