package com.tradingviewj.server.entity.replay.voice;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「盘面上多了一段语音」的广播（对应 Go 的 {@code replay.VoiceAddedPayload}）。
 *
 * <p>只有盘面这一份：语音是盘面数据，和有没有一条正在进行的交易记录无关。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class VoiceAddedPayload {

    private AudioClip audio;

    /** 这根 K 线**加完之后**的清单与计数（前端照它显示，不自己 +1） */
    private VoiceState voice;

    /** 整个实例的清单：多一根 K 线上的语音，界面上那个「其他 N 条」也得跟着变 */
    private List<AudioClip> voiceAll;
}
