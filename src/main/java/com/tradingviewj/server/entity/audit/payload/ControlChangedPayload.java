package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

/**
 * 接管权换人了（{@link ReplayAction#CONTROL_CHANGED}）。对应 Go 的 {@code ControlChangedPayload}。
 *
 * <p>{@code operator} 是换完之后<strong>新的</strong>操作者：人名的字符串，或机器人那个固定的
 * {@code "bot"}。带空串表示暂时谁都不操作（机器人在收尾时会先放手再退出）。
 *
 * <p>为什么操作权是「一个人」而不是「一组权限」：复盘只有一条时间线，两个人都能动手就会
 * 出现「我推一根、他推一根」的错位；换个用户操作是显式动作，前后都留痕。
 */
public record ControlChangedPayload(
        String operator
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.CONTROL_CHANGED;
    }
}
