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
                new BigDecimal("50000.00"), Map.of(CODE, symbol), List.of(open, closeOut));

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

    /** 5 个交易日、每天净赚 1000，走完最后一天并结账。 */
    private static PropFirmSession.Step driveFunded(String target) {
        Symbol symbol = Symbol.of(CODE, FOLDER, new BigDecimal("0.50"));
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
                new BigDecimal("50000.00"), Map.of(CODE, symbol), trades);
        for (String day : days) {
            session.onBar(CODE, bar(day + " 09:00", "4000.00", "4000.00", "4000.00", "4000.00"));
            session.onBar(CODE, bar(day + " 09:05", "4000.00", "4000.00", "4000.00", "4000.00"));
        }
        return session.finish();
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
