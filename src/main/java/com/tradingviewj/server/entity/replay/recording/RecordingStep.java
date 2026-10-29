package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.voice.AudioClip;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 交易记录里的一步（对应 Go 的 {@code replay.RecordingStep}）——**一步 = 一次真实操作**。
 *
 * <p>第 1 步是起点（kind = base，events 为空，它就是 base 本身）。之后每一条放行的广播开一步，
 * 所以「第 N 步」和「用户第 N 次动手」永远对得上。语音步特殊：events 为空、clip 放在 audio 里。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class RecordingStep {

    private int step;

    /** 这一步的真实发生时刻（Unix 毫秒）。秒不够用：同一秒里连着画两个图形很常见 */
    private long at;

    /** base / 动作名 / recording_audio */
    private String kind;

    /** 谁动的（human / bot / system） */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String by;

    /** 这一步发生时**当前那根 K 线**的时间 */
    private String time;

    /** 这一步发生后的游标（跳步时自检用） */
    private int cursor;

    /** 仅录音步：挂在这一步上的那一段 */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    private AudioClip audio;

    /** 通常只有 1 条（一条放行的广播 = 一步）；录音步为空数组，不是 null */
    private List<RecordingEvent> events;
}
