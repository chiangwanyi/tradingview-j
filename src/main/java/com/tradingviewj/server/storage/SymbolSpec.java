package com.tradingviewj.server.storage;

import java.math.BigDecimal;

/**
 * {@code data/_symbols.json} 里的一条标的记录，还没变成 {@code symbol} 表的一行。
 *
 * <p><strong>为什么单独一个 record，而不是直接给 {@code Symbol} 实体</strong>：解析清单是
 * 纯文件的事，不该拖上 JPA；中间隔一层，解析规则可以在没有数据库的情况下测。
 *
 * @param code       标的代码，即 {@code symbol.id}，例如 {@code CME_MINI:MES1!}
 * @param folder     {@code data/} 下的目录名，例如 {@code CME_MINI_MES1!}
 * @param tick       最小跳动；清单里没写就是 {@code null}
 * @param tickValue  一跳值多少美元；清单里没写就是 {@code null}
 * @param commission 每手每边的手续费，<strong>一定有值</strong>（0 = 免费）；
 *                   清单里没写这条的会被 {@link SymbolCatalog} 直接丢掉，不会走到这里
 */
public record SymbolSpec(String code, String folder, BigDecimal tick, BigDecimal tickValue,
                         BigDecimal commission) {
}
