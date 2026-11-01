package com.tradingviewj.server.service;

import org.junit.jupiter.api.Test;

import java.time.LocalDate;
import java.time.LocalDateTime;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 交易日口径的<strong>边界</strong>（用户 2026-11-01 确认）：
 *
 * <pre>
 *   交易日 D = [D 06:00, (D+1) 04:00)
 *   禁交易窗口 = [D 04:00, D 06:00]   ← 两端都禁
 * </pre>
 *
 * <p>两件事分开测，因为它们已经不是同一个问题了：
 * {@link PropFirmClock#tradingDayOf} 回答「这根归哪天」，{@link PropFirmClock#tradable}
 * 回答「这一根能不能下单」。06:00 就是那个把两者撕开的点 —— 它归新的一天，但不能交易。
 */
class PropFirmClockTest {

    private static void tradable(String at, boolean expected) {
        assertEquals(expected, PropFirmClock.tradable(at), at + " 能不能交易");
    }

    private static void dayOf(String at, LocalDate expected) {
        assertEquals(expected, PropFirmClock.tradingDayOf(at), at + " 属于哪个交易日");
    }

    /** 每天最早 06:05、最晚 03:55 —— 中间那 05 分钟一根都做不了。 */
    @Test
    void onlyOutsideTheClosedWindowIsTradable() {
        tradable("2026-10-29 03:55", true);
        tradable("2026-10-29 04:00", false);
        tradable("2026-10-29 04:05", false);
        tradable("2026-10-29 05:55", false);
        tradable("2026-10-29 06:00", false);
        tradable("2026-10-29 06:05", true);
        tradable("2026-10-29 23:55", true);
        tradable("2026-10-30 00:00", true);
    }

    /** 06:00 归新的一天（跨日结算就发生在它身上），但它在窗口里、不能交易。 */
    @Test
    void sixOClockBelongsToTheNewDayButIsNotTradable() {
        dayOf("2026-10-29 06:00", LocalDate.parse("2026-10-29"));
        assertFalse(PropFirmClock.tradable("2026-10-29 06:00"));
    }

    /** 凌晨 03:55 属于前一天；04:00 之后那段真空谁都不属于。 */
    @Test
    void theTailBeforeSixBelongsToThePreviousDay() {
        dayOf("2026-10-29 03:55", LocalDate.parse("2026-10-28"));
        dayOf("2026-10-29 00:00", LocalDate.parse("2026-10-28"));
        dayOf("2026-10-29 04:00", null);
        dayOf("2026-10-29 05:00", null);
        dayOf("2026-10-29 06:05", LocalDate.parse("2026-10-29"));
        dayOf("2026-10-29 12:00", LocalDate.parse("2026-10-29"));
    }

    /** 日终结算的时间戳就是次日 04:00 —— 04:00 正是「结束」这个时刻本身。 */
    @Test
    void sessionEndIsFourOClockOfTheNextDay() {
        assertEquals(LocalDateTime.parse("2026-10-29T04:00"),
                PropFirmClock.sessionEndOf(LocalDate.parse("2026-10-28")));
        assertNull(PropFirmClock.tradingDayOf(PropFirmClock.sessionEndOf(LocalDate.parse("2026-10-28"))));
    }
}
