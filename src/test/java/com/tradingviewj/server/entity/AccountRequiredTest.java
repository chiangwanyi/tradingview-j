package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.strategy.StrategyBot;
import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

/**
 * 「没有账户就不该有操作」的第一道闸：各实体的工厂方法。
 *
 * <p>这条规则有两层实现，缺一不可：
 *
 * <ul>
 *   <li>实体层（本测试）拦「根本没给账户」—— 编译期看不出来，只能在造对象那一刻拦；</li>
 *   <li>服务层（{@code PropFirmAccountService#requireActive}）拦「给了一个不存在 / 已结束的账户」。</li>
 * </ul>
 *
 * <p>{@code account_id} 在库里是 {@code NOT NULL}，所以这一层拦的不是「数据库能不能存 null」，
 * 而是「造对象时有没有给账户」—— 后者在编译期完全看不出来，只能靠工厂方法当场拦。
 * 删账户不会把归属置空（保留原 id），所以「列上非空」和「删账户不删数据」并不冲突。
 *
 * <p><strong>图形和语音不在这一列里</strong>：它们只挂在 K 线上（{@code symbolId} + {@code barTime}），
 * 不参与账户统计，所以工厂方法压根没有账户参数 —— 不是「可以不给」，是「没有这个口」。
 */
class AccountRequiredTest {

    private static final String ACCOUNT = "account-1";
    private static final String SYMBOL = "CME_MINI:MNQ1!";

    @Test
    void everyOperationRefusesToExistWithoutAnAccount() {
        assertThrows(IllegalArgumentException.class,
                () -> Order.place(null, SYMBOL, Side.BUY, Order.Type.LIMIT,
                        new BigDecimal("21250"), BigDecimal.ONE, "2026-10-16 12:00", null));

        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(null, SYMBOL, "order-1", "2026-10-16 12:05", 1,
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));

        assertThrows(IllegalArgumentException.class,
                () -> StrategyBot.start(null, SYMBOL, "strategy-1", BigDecimal.ONE, 300));

        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(null, "考核", SYMBOL, "2026-10-16 12:00", 0, Actor.HUMAN, "jiang"));

        // 操作记录也没有例外：审计流水同样必须归属一个账户
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(null, SYMBOL, "2026-10-16 12:00", 0,
                        Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));

        // 有账户就能造出来，说明拦截的是「空」而不是「所有」
        assertDoesNotThrow(() -> TradeRecord.open(ACCOUNT, "考核", SYMBOL, "2026-10-16 12:00", 0,
                Actor.HUMAN, "jiang"));
        assertDoesNotThrow(() -> AuditEvent.of(ACCOUNT, SYMBOL, "2026-10-16 12:00", 0,
                Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
    }

    /**
     * 账户不再绑定单个品种（一个 PF 账户可以同时做 MES / MNQ / MGC），
     * 所以「品种」变成<strong>每条操作自己的必填项</strong>。
     *
     * <p>少了它，图形只有一个 {@code {barTime, price}} 的坐标、成交只有一个时间，
     * 谁都不知道该去读哪份 CSV —— 而这件事同样在编译期完全看不出来。
     */
    @Test
    void everyOperationRefusesToExistWithoutASymbol() {
        Anchor a = new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0);

        assertThrows(IllegalArgumentException.class,
                () -> Drawing.of(null, Drawing.Kind.TREND, a, a, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> Drawing.of("  ", Drawing.Kind.TREND, a, a, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> AudioClip.record(null, "2026-10-16 12:00", 0, "x.wav", 1000, 1024, Actor.HUMAN));
        assertThrows(IllegalArgumentException.class,
                () -> Order.place(ACCOUNT, " ", Side.BUY, Order.Type.LIMIT,
                        new BigDecimal("21250"), BigDecimal.ONE, "2026-10-16 12:00", null));
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, null, "order-1", "2026-10-16 12:05", 1,
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
        assertThrows(IllegalArgumentException.class,
                () -> StrategyBot.start(ACCOUNT, null, "strategy-1", BigDecimal.ONE, 300));
        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(ACCOUNT, "考核", null, "2026-10-16 12:00", 0, Actor.HUMAN, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(ACCOUNT, null, "2026-10-16 12:00", 0,
                        Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
    }

    /**
     * 操作记录必须能定位到「哪根 K 线、游标第几根、第几步」。
     *
     * <p>这两样是逐 K 回放能不能跑起来的全部前提：没有 {@code bar_time} 就不知道该读哪份
     * CSV 的哪一根（也就找不回进度），没有 {@code seq} 就分不出同一根 K 线上多次操作的先后。
     * 它们曾经只存在于 {@code payload_json} 里 —— 那意味着每次回放都要把整段流水
     * 拉回内存再逐条解析 JSON，一账户多品种时还过滤不了。
     *
     * <p>这里<strong>没有下标</strong>：回放定位只认时间。
     */
    @Test
    void auditEventMustCarryItsPosition() {
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(ACCOUNT, SYMBOL, null, 0,
                        Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(ACCOUNT, SYMBOL, "  ", 0,
                        Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(ACCOUNT, SYMBOL, "2026-10-16 12:00", -1,
                        Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));

        // 起点事件没有负载也是合法的：起点盘面由快照表按时间过滤现算，不复制一份进 payload
        assertDoesNotThrow(() -> AuditEvent.base(ACCOUNT, SYMBOL, "2026-10-16 12:00", 0, Actor.HUMAN));
    }

    /**
     * 交易记录的区间不能是反的：{@code endSeq < startSeq} 意味着「平仓发生在开仓之前」，
     * 回放会读到空区间，而且这个错误在界面上只表现为「这一步什么都没有」，查不出来。
     */
    @Test
    void recordRefusesAnInvertedEventRange() {
        TradeRecord record = TradeRecord.open(ACCOUNT, "考核", SYMBOL, "2026-10-16 12:00", 7,
                Actor.HUMAN, "jiang");
        assertThrows(IllegalArgumentException.class,
                () -> record.closeAt("2026-10-16 13:00", new BigDecimal("21260"),
                        new BigDecimal("20"), 6));
        assertDoesNotThrow(() -> record.closeAt("2026-10-16 13:00", new BigDecimal("21260"),
                new BigDecimal("20"), 7), "起点和终点相同是合法的：开仓与平仓之间没有任何操作");
    }

    /**
     * 成交必须带手续费：{@code null} 会在算净盈亏时变成 0，让一个漏配的标的看着像免手续费的。
     * {@code 0} 是合法的（CL 就是免手续费），所以这里拦的是「没给」而不是「给了 0」。
     */
    @Test
    void fillRefusesToExistWithoutCommission() {
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05", 1,
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, null));
        assertDoesNotThrow(() -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05", 1,
                Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
    }

    /**
     * 成交必须带正数流水号：同一根 K 线上多笔成交的先后全靠它。
     *
     * <p>允许 0 或负数的话，写库顺序会退化成「谁先插入谁在前」或者「按 UUID 字典序」，
     * 均价和已实现盈亏会在反手、部分平仓这些边界上算错 —— 而且每次重启错得还不一样。
     */
    @Test
    void fillRefusesToExistWithoutPositiveSeq() {
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05", 0,
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05", -3,
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
        assertDoesNotThrow(() -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05", 1,
                Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
    }

    /**
     * 交易记录必须带<strong>账户名快照</strong>：账户行被删之后，这是唯一还记得
     * 「当初算在谁头上」的地方。没有它，「删账户后仍能在另一个页面看所有记录」
     * 就只剩一串指向已消失账户的 id。
     */
    @Test
    void recordRefusesToExistWithoutAccountNameSnapshot() {
        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(ACCOUNT, null, SYMBOL, "2026-10-16 12:00", 0,
                        Actor.HUMAN, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(ACCOUNT, "  ", SYMBOL, "2026-10-16 12:00", 0,
                        Actor.HUMAN, "jiang"));

        // 重挂账户时 id 和名字必须一起换：只换 id 会得到「归属是新账户、显示旧名字」的记录
        TradeRecord record = TradeRecord.open(ACCOUNT, "旧考核", SYMBOL, "2026-10-16 12:00", 0,
                Actor.HUMAN, "jiang");
        assertThrows(IllegalArgumentException.class, () -> record.attachTo("account-2", null));
        record.attachTo("account-2", "新考核");
        assertEquals("account-2", record.getAccountId());
        assertEquals("新考核", record.getAccountName());
    }
}
