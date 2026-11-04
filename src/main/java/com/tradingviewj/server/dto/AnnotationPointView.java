package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 图形上的一个锚点<strong>在协议里</strong>的样子（前端 {@code webapp/src/types/protocol.ts}
 * 的 {@code Point}）。
 *
 * <p>注意字段名不叫 {@code barTime} 而叫 {@link #time}：实体 {@code Anchor} 用的是
 * {@code barTime}（因为它总是一根 K 线），但协议上这个位置已经带上了 {@link #offset}
 * ——「第几根 + 往右推几格」是一个屏幕坐标，不一定落在某根 K 线上。
 * 叫 {@code time} 是 Go 版沿用的协议名，改名会让前端所有引用跟着动，不值得。
 *
 * @param time   锚定的那根 K 线（{@code yyyy-MM-dd HH:mm}，必须是已揭示的数据）
 * @param price  纵坐标价格
 * @param offset 从锚点往右再推几根：0 = 就在那根 K 线中心
 */
public record AnnotationPointView(
        String time,
        BigDecimal price,
        int offset
) {
}
