package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarView;

import java.math.BigDecimal;
import java.util.List;

/**
 * 一条下行广播（后端 → 前端）：<strong>盘面被改了</strong>。
 *
 * <p>口径照 Go 版：<strong>改盘面本身即广播</strong>，而且<strong>连发起方也收</strong>。
 * 发送方不在本地先改一遍再等广播回来，是因为那样要维护两份「同一件事的两种推算」——
 * 本地那份一旦和后端算出来的不一样（少一根、价格取整差一跳），界面就成了一个没人能解释的状态。
 * 一份数据、一个来源，慢一个往返换来的是永远对得上。
 *
 * @param action  动作名：{@code ReplayAction} 常量名转小写（{@code bar_advanced}）。
 *                换算只在输出侧做，库里存的是 Java 常量名
 * @param payload 这次动作的负载，形状与 {@code AuditEvent.payloadJson} 里那份<strong>完全一致</strong>
 * @param by      谁做的：{@code human} / {@code bot} / {@code system}
 * @param seq     这条流水的账户内序号。前端按它排序，也能一眼看出有没有断档
 */
public record EventMessage(
        String type,
        String action,
        Object payload,
        String by,
        int seq
) {

    public static EventMessage of(String action, Object payload, String by, int seq) {
        return new EventMessage("event", action, payload, by, seq);
    }
}
