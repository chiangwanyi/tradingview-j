package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「这根 K 线上的语音全被清掉」的广播（对应 Go 的 {@code replay.VoiceClearedPayload}）。
 *
 * <p>removed 带着被删掉的那几条（文件名要一起带：前端可能正在播其中一条，得停下）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class VoiceClearedPayload {

    private List<AudioClip> removed;

    private VoiceState voice;

    private List<AudioClip> voiceAll;
}
