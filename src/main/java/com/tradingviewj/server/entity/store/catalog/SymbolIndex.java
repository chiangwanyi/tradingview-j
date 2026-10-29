package com.tradingviewj.server.entity.store.catalog;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.Map;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * {@code <root>/_symbols.json} 的完整内容（对应 Go 的 {@code store.symbolIndex}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class SymbolIndex {

    private int version;

    /** 标的 -> 登记项 */
    private Map<String, SymRec> symbols;
}
