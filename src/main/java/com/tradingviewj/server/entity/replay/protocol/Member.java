package com.tradingviewj.server.entity.replay.protocol;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 实例里的一个人（对应 Go 的 {@code replay.Member}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Member {

    private String clientId;

    private String name;

    private boolean operator;

    private boolean connected;
}
