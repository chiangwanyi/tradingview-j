package com.tradingviewj.server.entity;

import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.indicator.Indicator;
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
 */
class AccountRequiredTest {

    private static final String ACCOUNT = "account-1";
    private static final String SYMBOL = "CME_MINI:MNQ1!";

    @Test
    void everyOperationRefusesToExistWithoutAnAccount() {
        Anchor a = new Anchor("2026-10-16 12:00", new BigDecimal("21250.25"), 0);

        assertThrows(IllegalArgumentException.class,
                () -> Drawing.of(null, SYMBOL, Drawing.Kind.TREND, a, a, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> Drawing.of("  ", SYMBOL, Drawing.Kind.TREND, a, a, "jiang"));

        assertThrows(IllegalArgumentException.class,
                () -> Order.place(null, SYMBOL, Side.BUY, Order.Type.LIMIT,
                        new BigDecimal("21250"), BigDecimal.ONE, "2026-10-16 12:00", 0, null));

        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(null, SYMBOL, "order-1", "2026-10-16 12:05",
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));

        assertThrows(IllegalArgumentException.class,
                () -> AudioClip.record(null, SYMBOL, "2026-10-16 12:00", 0, "x.wav", 1000, 1024, Actor.HUMAN));

        assertThrows(IllegalArgumentException.class,
                () -> Indicator.of(null, SYMBOL, Indicator.Type.EMA, 20, Indicator.Source.CLOSE));

        assertThrows(IllegalArgumentException.class,
                () -> StrategyBot.start(null, SYMBOL, "strategy-1", BigDecimal.ONE, 300));

        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(null, SYMBOL, "2026-10-16 12:00", Actor.HUMAN, "jiang"));

        // 操作记录也没有例外：审计流水同样必须归属一个账户
        assertThrows(IllegalArgumentException.class,
                () -> AuditEvent.of(null, 0, Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));

        // 有账户就能造出来，说明拦截的是「空」而不是「所有」
        assertDoesNotThrow(() -> Drawing.of(ACCOUNT, SYMBOL, Drawing.Kind.TREND, a, a, "jiang"));
        assertDoesNotThrow(() -> TradeRecord.open(ACCOUNT, SYMBOL, "2026-10-16 12:00", Actor.HUMAN, "jiang"));
        assertDoesNotThrow(() -> AuditEvent.of(ACCOUNT, 0, Actor.HUMAN, ReplayAction.BAR_ADVANCED, "{}"));
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
                () -> Drawing.of(ACCOUNT, null, Drawing.Kind.TREND, a, a, "jiang"));
        assertThrows(IllegalArgumentException.class,
                () -> Order.place(ACCOUNT, " ", Side.BUY, Order.Type.LIMIT,
                        new BigDecimal("21250"), BigDecimal.ONE, "2026-10-16 12:00", 0, null));
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, null, "order-1", "2026-10-16 12:05",
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
        assertThrows(IllegalArgumentException.class,
                () -> AudioClip.record(ACCOUNT, null, "2026-10-16 12:00", 0, "x.wav", 1000, 1024, Actor.HUMAN));
        assertThrows(IllegalArgumentException.class,
                () -> Indicator.of(ACCOUNT, null, Indicator.Type.EMA, 20, Indicator.Source.CLOSE));
        assertThrows(IllegalArgumentException.class,
                () -> StrategyBot.start(ACCOUNT, null, "strategy-1", BigDecimal.ONE, 300));
        assertThrows(IllegalArgumentException.class,
                () -> TradeRecord.open(ACCOUNT, null, "2026-10-16 12:00", Actor.HUMAN, "jiang"));
    }

    /**
     * 成交必须带手续费：{@code null} 会在算净盈亏时变成 0，让一个漏配的标的看着像免手续费的。
     * {@code 0} 是合法的（CL 就是免手续费），所以这里拦的是「没给」而不是「给了 0」。
     */
    @Test
    void fillRefusesToExistWithoutCommission() {
        assertThrows(IllegalArgumentException.class,
                () -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05",
                        Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, null));
        assertDoesNotThrow(() -> Trade.fill(ACCOUNT, SYMBOL, "order-1", "2026-10-16 12:05",
                Side.BUY, new BigDecimal("21250"), BigDecimal.ONE, BigDecimal.ZERO, BigDecimal.ZERO));
    }
}
