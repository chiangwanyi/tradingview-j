package com.tradingviewj.server.entity.replay.voice;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 当前这根 K 线上的语音讲解清单与计数（对应 Go 的 {@code replay.VoiceState}）。
 *
 * <p>永远是对象、不是 null：语音和绘图一样是盘面的一部分，没录过就是空清单。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class VoiceState {

    private String barTime;

    private List<AudioClip> clips;

    /** 这根 K 线上共几条 */
    private int total;

    /** 这根 K 线最多能录几条 */
    private int max;
}
