package com.tradingviewj.server.entity.replay.protocol;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 只回给发起方的错误（对应 Go 的 {@code replay.ErrorMsg}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class ErrorMsg {

    private String type;

    private String message;

    /** 是哪条请求失败了 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String request;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String instanceId;
}
