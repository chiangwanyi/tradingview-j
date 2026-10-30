package com.tradingviewj.server.dto;

/**
 * 步进请求体：把账户的「盘面最新时间」推到某一根 K 线。
 *
 * <p>只有 {@code barTime} 一个字段，格式固定 {@code yyyy-MM-dd HH:mm}（本地时区明文，
 * 和 CSV 时间列、{@code prop_firm_account.last_bar_time} 同一个口径）。
 *
 * <p>为什么是「时间」而不是「第几根」：下标是位置，日文件重抓 / 补一根 / 删一根坏行都会让
 * 同一个下标悄悄指向另一根 K 线，而且不报错；时间永远指向原来那一根。
 */
public record PropFirmCursorRequest(String barTime) {
}
