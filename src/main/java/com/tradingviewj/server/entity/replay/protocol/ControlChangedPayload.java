package com.tradingviewj.server.entity.replay.protocol;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 操作权换了人（对应 Go 的 {@code replay.ControlChangedPayload}）；空 = 没人接管。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class ControlChangedPayload {

    private String operator;
}
