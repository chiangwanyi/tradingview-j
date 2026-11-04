package com.tradingviewj.server.dto;

/**
 * 清空交易信息的结果：4 张表各删掉几行。
 *
 * <p>按表分开返回而不是只给一个总数，是因为这 4 张表的<strong>关系</strong>本身就是
 * 「挂单 → 成交 → 交易记录 → 记录标签」的一条链。用户看到
 * 「挂单 12 / 成交 8 / 记录 3 / 标签 6」时，能立刻判断这次清空是否清在了他以为的范围里；
 * 只给「共 28 行」的话，数字对不上时没人知道是哪张表出了问题。
 *
 * <p>{@code tagLinks} 数的是 {@code trade_record_tag} 的<strong>关联行</strong>
 * （谁打了哪个标签），不是标签本身 —— 标签字典 {@code trade_tag} 是用户自己攒的本子，
 * 交易清空了它还在（2026-11-01 拆分）。所以这个字段叫 {@code tagLinks} 而不是 {@code tags}：
 * 一个名字如果会让人以为「标签被删了」，那就该换一个。
 */
public record ClearTradeDataResponse(long orders, long trades, long records, long tagLinks) {
}
