package com.tradingviewj.server.service;

import org.junit.jupiter.api.Test;

import java.time.LocalDate;
import java.time.LocalDateTime;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 交易日口径：{@code [06:00, 次日 04:45)} 算一天，04:45–06:00 的 75 分钟<strong>谁都不算</strong>。
 *
 * <p>这条口径一改，日历、日盈亏、一致性分母、盈利日计数四处会一起变，
 * 所以它必须只有一份实现，并且被钉死在这里。
 */
class PropFirmClockTest {

    @Test
    void earlyMorningBelongsToPreviousTradingDay() {
        // 凌晨 3:10 是"昨天那场"的尾巴 —— 期货夜盘跨过零点
        assertEquals(LocalDate.of(2026, 10, 28),
                PropFirmClock.tradingDayOf("2026-10-29 03:10"));
        // 04:44 还算前一天的
        assertEquals(LocalDate.of(2026, 10, 28),
                PropFirmClock.tradingDayOf("2026-10-29 04:44"));
    }

    @Test
    void sessionStartsAtSix() {
        assertEquals(LocalDate.of(2026, 10, 29),
                PropFirmClock.tradingDayOf("2026-10-29 06:00"));
        assertEquals(LocalDate.of(2026, 10, 29),
                PropFirmClock.tradingDayOf("2026-10-29 23:55"));
    }

    @Test
    void gapBetweenEndAndStartBelongsToNoTradingDay() {
        // 04:45 整点开始不能交易；05:59 也还不能
        assertNull(PropFirmClock.tradingDayOf("2026-10-29 04:45"));
        assertNull(PropFirmClock.tradingDayOf("2026-10-29 05:00"));
        assertNull(PropFirmClock.tradingDayOf("2026-10-29 05:59"));

        assertFalse(PropFirmClock.tradable("2026-10-29 04:45"));
        assertFalse(PropFirmClock.tradable("2026-10-29 05:30"));
        assertTrue(PropFirmClock.tradable("2026-10-29 04:44"));
        assertTrue(PropFirmClock.tradable("2026-10-29 06:00"));
    }

    @Test
    void sessionEndIsNextMorningAtQuarterToFive() {
        assertEquals(LocalDateTime.of(2026, 10, 30, 4, 45),
                PropFirmClock.sessionEndOf(LocalDate.of(2026, 10, 29)));
    }

    @Test
    void badFormatFailsLoudly() {
        // 静默跳过会让"今天怎么少了一半 K 线"变成一个查不出来的问题
        assertThrows(IllegalArgumentException.class, () -> PropFirmClock.tradingDayOf("2026/10/29 10:00"));
        assertThrows(IllegalArgumentException.class, () -> PropFirmClock.tradingDayOf(""));
        assertThrows(IllegalArgumentException.class, () -> PropFirmClock.tradingDayOf((String) null));
    }
}
