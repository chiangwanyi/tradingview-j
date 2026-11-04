package com.tradingviewj.server.dto;

import java.time.Instant;

/**
 * 操作记录列表里的一行。
 *
 * <p>{@code action} / {@code actor} 都是<strong>小写协议串</strong>（{@code order_placed} /
 * {@code human}），和 WS 广播里的一致 —— 前端一份映射表就能两边通用。
 *
 * <p>{@code payloadJson} 不往外发：列表里只是「扫一眼发生了什么」，
 * 负载是回放时才需要解析的东西。
 */
public record AuditEventView(
        String id,
        String accountId,
        String accountName,
        String symbolId,
        String barTime,
        int seq,
        String actor,
        String action,
        Instant createdAt
) {
}
