package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 某根 K 线上的语音被清空（{@link ReplayAction#VOICE_CLEARED}）。对应 Go 的
 * {@code VoiceClearedPayload}。
 *
 * <p>{@code removed} 带着<strong>被删掉的那几条</strong>，而不是一个数量：前端可能正在播其中
 * 一条，拿到文件名才能把它停下来。数量对这件事没用。
 *
 * <p>{@code voice} / {@code voiceAll} 与 {@link VoiceAddedPayload} 同义（清完之后的状态），
 * 这样「加」和「清」两个方向在前端可以走同一个更新函数。
 */
public record VoiceClearedPayload(
        List<AudioClip> removed,
        VoiceState voice,
        List<AudioClip> voiceAll
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.VOICE_CLEARED;
    }
}
