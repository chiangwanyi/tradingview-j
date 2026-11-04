package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.enums.ReplayAction;

import java.util.List;

/**
 * 盘面上多了一段语音（{@link ReplayAction#VOICE_ADDED}）。对应 Go 的 {@code VoiceAddedPayload}。
 *
 * <p>三份内容各有各的用处，缺一个界面就得自己算：
 *
 * <ul>
 *   <li>{@code audio} —— 刚录的那一段本身（文件名、时长、挂在哪根 K 线上）；</li>
 *   <li>{@code voice} —— 这根 K 线<strong>加完之后</strong>的清单与计数，
 *       前端照它显示「本根 3/9」，不自己 +1（否则和服务端上限一改就对不上）；</li>
 *   <li>{@code voiceAll} —— 整个实例的清单。复盘只能往前走、不能倒回，没有这份全量清单，
 *       早先几根上讲的语音会变成「听得见总数、点不到」。</li>
 * </ul>
 *
 * <p>它和 {@link RecordingAudioPayload} 别混：那条是「交易记录里的语音步」，
 * 这条描述的是<strong>盘面</strong>上多了一段声音，和有没有在记一笔交易无关。
 */
public record VoiceAddedPayload(
        AudioClip audio,
        VoiceState voice,
        List<AudioClip> voiceAll
) implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.VOICE_ADDED;
    }
}
