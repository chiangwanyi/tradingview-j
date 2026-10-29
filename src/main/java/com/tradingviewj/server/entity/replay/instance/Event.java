package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一条操作记录（对应 Go 的 {@code replay.Event}）。
 *
 * <p>所有会改状态的操作都会广播一条，广播内容与落库内容一致，
 * 所以「谁在什么时候做了什么」在后端是完整的（便于演示回看与排查）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Event {

    private int seq;

    /** Unix 毫秒 */
    private long at;

    /** 谁动的（human / bot / system） */
    private String by;

    private String action;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private Object payload;
}
