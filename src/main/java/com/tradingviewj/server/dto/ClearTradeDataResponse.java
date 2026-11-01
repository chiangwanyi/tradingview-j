package com.tradingviewj.server.dto;

/**
 * 清空交易信息的结果：4 张表各删掉几行。
 *
 * <p>按表分开返回而不是只给一个总数，是因为这 4 张表的<strong>关系</strong>本身就是
 * 「挂单 → 成交 → 交易记录 → 记录标签」的一条链。用户看到
 * 「挂单 12 / 成交 8 / 记录 3 / 标签 5」时，能立刻判断这次清空是否清在了他以为的范围里；
 * 只给「共 28 行」的话，数字对不上时没人知道是哪张表出了问题。
 *
 * <p>{@code tags} 是 {@code trade_record_tag}（交易记录上的自定义标签），
 * 它靠 {@code record_id} 挂在记录上，所以永远小于等于「记录被打过标签的次数」，
 * 和 {@code records} 不是 1:1。
 */
public record ClearTradeDataResponse(long orders, long trades, long records, long tags) {
}
