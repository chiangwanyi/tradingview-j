package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一条广播（对应 Go 的 {@code replay.EventMsg}）：改盘面本身即广播，发全量、带 by。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class EventMsg {

    private String type;

    private String instanceId;

    private int seq;

    private long at;

    private String by;

    private String action;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Object payload;
}
