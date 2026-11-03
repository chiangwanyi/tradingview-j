package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.storage.Bar;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.util.List;
import java.util.Map;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertEquals;

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
    private static Symbol symbol() {
        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
        symbol.setTick(new BigDecimal("0.25"));
        symbol.setTickValue(new BigDecimal("1.25"));
        return symbol;
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
