package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一条被录下来的操作（对应 Go 的 {@code replay.RecordingEvent}），字段与广播一一对应。
 *
 * <p>payload 保持原始 JSON、不重新构造：任何「翻译一层」的做法都会让回放和实时出现细微差异。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class RecordingEvent {

    private int seq;

    private long at;

    /** human / human2 / bot */
    private String by;

    private String action;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Object payload;
}
