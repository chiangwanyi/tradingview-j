package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.entity.voice.AudioClip;

import java.util.List;

/**
 * <strong>某一根 K 线</strong>上的语音状态（支撑类型，不是动作）。对应 Go 的 {@code VoiceState}。
 *
 * <p>{@code clips} 是那根 K 线的语音清单，{@code total} / {@code max} 是「本根 3/9」那个
 * 角标的两个数 —— 前端照它们显示，不自己数。
 *
 * <p>为什么把上限 {@code max} 一起发下来：上限是<strong>服务端</strong>的规则，前端再抄一遍
 * 迟早会和后端对不上，而对不上时用户只会看到「按钮还能点，点了报错」。
 *
 * <p>它被 {@code bar_advanced} / {@code voice_added} / {@code voice_cleared} 三条动作共用；
 * 整个实例的清单是另一个字段（{@code voiceAll}），两者别混：这里只装当前这根。
 */
public record VoiceState(
        String barTime,
        List<AudioClip> clips,
        int total,
        int max
) {
}
