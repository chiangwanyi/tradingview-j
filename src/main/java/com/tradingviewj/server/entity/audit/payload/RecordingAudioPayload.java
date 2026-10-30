package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

/**
 * 交易记录里的「语音步」（{@link ReplayAction#RECORDING_AUDIO}）—— <strong>没有负载</strong>。
 * 对应 Go 的 {@code ActRecordingAudio}：那一步的录音挂在 {@code RecordingStep.audio} 上，
 * 不经过广播负载。
 *
 * <p>为什么留着这个空 record：动作集合要保持封闭（见 {@link ReplayActionPayload}），
 * {@code switch} 覆盖 20 个动作时不必写旁支。
 *
 * <p>为什么这个动作还活着：它在库里有历史数据（{@code RECORDING_AUDIO} 常量名已落进
 * {@code audit_event.action}），删掉会让老记录读不回来。但<strong>目前没有写入方</strong>
 * —— 语音现在按 {@code audio_clip.bar_time} 落在区间内取，不再单独占一条流水。
 */
public record RecordingAudioPayload() implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.RECORDING_AUDIO;
    }
}
