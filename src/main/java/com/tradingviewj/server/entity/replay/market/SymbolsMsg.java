package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 可选标的清单（对应 Go 的 {@code replay.SymbolsMsg}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class SymbolsMsg {

    private String type;

    private List<SymbolInfo> symbols;
}
