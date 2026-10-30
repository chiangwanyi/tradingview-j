package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.trade.Order;

import java.math.BigDecimal;
import java.util.Locale;

/**
 * 一张挂单<strong>在协议里</strong>的样子（前端 {@code webapp/src/types/protocol.ts} 的 {@code Order}）。
 *
 * <h2>为什么不让 {@link Order} 实体直接上线</h2>
 *
 * <p>实体是<strong>库里的形状</strong>：枚举存 Java 常量名（{@code BUY} / {@code LIMIT}）、
 * 时间列叫 {@code placedAtBar}。协议是<strong>客户端的形状</strong>：小写串（{@code buy}）、
 * 时间叫 {@code placedAt}。两者的换算必须有且只有一处，否则前端就要知道
 * 「哪些字段要大写、哪些要小写」—— 那种知识会在某一端改字段名时悄悄失配，
 * 而表现只是「挂单线画不出来」。
 *
 * <p>也<strong>不</strong>在实体上加 {@code @JsonProperty}：那会让「改一次协议」
 * 变成「改一张表」，而且库里存的枚举名是审计口径，不该为客户端格式让步
 * （见 {@code schema.sql} 第 5 条约定）。
 *
 * <p>刻意<strong>不发</strong> {@code accountId} / {@code symbolId}：一条广播只属于一个账户、
 * 一块盘面（连接的房间和快照已经说明了这两件事）。每个订单再各带一份，
 * 前端就会开始「相信订单上那个账户 id」—— 而它与连接上的账户不一致时没人能解释。
 *
 * @param side   {@code buy} / {@code sell}
 * @param kind   {@code limit} / {@code stop} / {@code market}
 * @param status {@code open} / {@code filled} / {@code cancelled}
 * @param placedAt 下单那一刻盘面停在哪根 K 线（{@code yyyy-MM-dd HH:mm}）
 * @param filledAt 成交那一刻的 K 线时间；{@code null} = 没成交（全局 non_null 会省掉这个键）
 * @param group    OCO 组号；{@code null} = 独立挂单
 */
public record OrderView(
        String id,
        String side,
        String kind,
        String status,
        BigDecimal price,
        BigDecimal quantity,
        String placedAt,
        String filledAt,
        BigDecimal fillPrice,
        String group
) {

    /** 实体的枚举 → 协议串。{@code toLowerCase(Locale.ROOT)} 和 {@code ReplayAction#wire()} 同一套理由。 */
    public static OrderView of(Order order) {
        return new OrderView(
                order.getId(),
                lower(order.getSide()),
                lower(order.getType()),
                lower(order.getStatus()),
                order.getPrice(),
                order.getQuantity(),
                order.getPlacedAtBar(),
                order.getFilledAtBar(),
                order.getFillPrice(),
                order.getOcoGroup());
    }

    private static String lower(Enum<?> value) {
        return value == null ? null : value.name().toLowerCase(Locale.ROOT);
    }
}
