package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.voice.AudioClip;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 在回放末尾那根 K 线上录音 / 清空的结果（对应 Go 的 {@code replay.RecordingVoiceMsg}）。
 *
 * <p>新增时 audio 与 step 成对出现；清空时只有 removed。清空之后步号会重排，
 * 所以这里不回传整份 steps，而是由前端按同一条规则（step = 下标 + 1）就地重排。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class RecordingVoiceMsg {

    private String type;

    private String recordingId;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private AudioClip audio;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    private RecordingStep step;

    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    private List<AudioClip> removed;

    private RecordingSummary recording;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String warning;
}
