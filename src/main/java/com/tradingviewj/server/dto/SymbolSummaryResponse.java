package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 交易页标的选择器里的一行。
 *
 * <p>字段全部来自 {@code data/_symbols.json}（经 {@link com.tradingviewj.server.storage.SymbolCatalog}），
 * 加上磁盘上数出来的日文件个数与首尾日期 —— <strong>不查数据库</strong>：
 * 这个接口是给画布取数用的，标的清单的唯一来源就是抓取侧写的那个文件。
 *
 * @param code       标的代码，例如 {@code CME_MINI:MES1!}
 * @param folder     {@code data/} 下的目录名
 * @param tick       最小跳动；清单里没写就是 null（前端按两位小数兜底）
 * @param tickValue  一跳值多少美元；清单里没写就是 null
 * @param commission 每手每边的手续费（0 = 免费）
 * @param days       这个标的磁盘上有多少个日文件
 * @param firstDay   最早的日文件日期（{@code yyyy-MM-dd}）；没有文件时为 null
 * @param lastDay    最新的日文件日期；没有文件时为 null
 */
public record SymbolSummaryResponse(
        String code,
        String folder,
        BigDecimal tick,
        BigDecimal tickValue,
        BigDecimal commission,
        int days,
        String firstDay,
        String lastDay
) {
}
