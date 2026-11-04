package com.tradingviewj.server.enums;

import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 钉住「枚举 ↔ 协议串」这一层换算。
 *
 * <p>为什么要测：{@code wire()} 现在是<strong>双向</strong>的字面量来源 ——
 * 下行广播（{@code EventMessage.action}）和上行命令（{@code WsRequest.type}）都用它。
 * 换算一旦和前端 {@code webapp/src/types/protocol.ts} 的 {@code ACTION} 表不一致，
 * 表现是「点了没反应」或者「收到一条认不出的广播」，而这中间没有任何编译期报错。
 */
class ReplayActionTest {

    @Test
    void wireIsTheLowercaseConstantName() {
        assertEquals("bar_advanced", ReplayAction.BAR_ADVANCED.wire());
        assertEquals("order_placed", ReplayAction.ORDER_PLACED.wire());
        assertEquals("flat_all", ReplayAction.FLAT_ALL.wire());
    }

    @Test
    void ofWireRoundTripsEveryValue() {
        for (ReplayAction action : ReplayAction.values()) {
            assertSame(action, ReplayAction.ofWire(action.wire()).orElseThrow(),
                    action + " 的协议串翻不回自己");
        }
    }

    @Test
    void wireValuesAreUniqueAndLowerCase() {
        Map<String, ReplayAction> seen = new HashMap<>();
        for (ReplayAction action : ReplayAction.values()) {
            String wire = action.wire();
            assertEquals(wire.toLowerCase(Locale.ROOT), wire, action + " 的协议串必须全小写");
            assertTrue(wire.matches("[a-z0-9_]+"), action + " 的协议串只能是小写字母/数字/下划线：" + wire);
            ReplayAction clash = seen.put(wire, action);
            // 小写后撞车的两个动作，前端 pick reducer 分支时永远只能命中一个 ——
            // 那种 bug 在收发现场查不出来，只能在这里红
            assertTrue(clash == null, action + " 与 " + clash + " 的协议串撞了：" + wire);
        }
    }

    @Test
    void ofWireRejectsEverythingElse() {
        assertTrue(ReplayAction.ofWire(null).isEmpty());
        assertTrue(ReplayAction.ofWire("").isEmpty());
        assertTrue(ReplayAction.ofWire("bar_advanced ").isEmpty());
        // 库里存的是常量名，协议上是小写串，两者不能混着用
        assertTrue(ReplayAction.ofWire("BAR_ADVANCED").isEmpty());
        // 旧命令名（少一个 d）必须落到「不认识」，否则它会静默变成“已接通”
        assertTrue(ReplayAction.ofWire("order_place").isEmpty());
    }

    /**
     * 三条元命令不得出现在枚举里：{@code hello}/{@code step}/{@code ping} 是连接管理，
     * 不产生审计事实。它们若被 {@code ofWire} 认出来，{@code TradeSocketHandler} 就会
     * 把「推进一根」当成「有动作但没接通」给拒掉。
     */
    @Test
    void metaCommandsAreNotReplayActions() {
        assertTrue(ReplayAction.ofWire("hello").isEmpty());
        assertTrue(ReplayAction.ofWire("step").isEmpty());
        assertTrue(ReplayAction.ofWire("ping").isEmpty());
    }
}
