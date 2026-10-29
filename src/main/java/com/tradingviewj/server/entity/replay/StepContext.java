package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「最近一步发生时的盘面位置」（对应 Go 的 {@code replay.StepContext}）。
 *
 * <p>语音不是广播驱动的那一步（它自己开段），所以不能像别的动作那样从参数里拿位置。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class StepContext {

    /** 当前那根 K 线的明文时间（空 = 还没有 K 线） */
    private String time;

    private int cursor;
}
