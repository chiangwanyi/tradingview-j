package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.voice.AudioClip;

import java.util.Locale;

/**
 * 一段语音讲解的<strong>协议视图</strong>（对应前端 {@code AudioClip}）。
 *
 * <p>为什么不直接发实体：实体带 {@code symbolId} / {@code fileName} / {@code sizeBytes} /
 * {@code updatedAt} 这些协议里用不到的东西，而字段名也不一样（{@code barTime} ↔ {@code time}）。
 * 前端 {@code AudioClip} 只认这一份，加字段时两边一起改，一眼能对上。
 *
 * <p>{@code createdAt} 是<strong>Unix 秒</strong>（前端用它排同一根上的先后），
 * {@code by} 是 {@code actor} 的小写串（协议约定，见 {@code Actor}）。
 */
public record AudioClipView(
        String id,
        /** 挂在哪根 K 线上（K 线时间，存储键） */
        String time,
        /** 同一根上的第几段（从 0 起） */
        int seq,
        int durationMs,
        /** 相对路径（{@code <标的目录>/<日期>/<uuid>.wav}），音频本身走 {@code GET /api/audio/<id>} */
        String file,
        long size,
        long createdAt,
        /** human / bot / system */
        String by
) {

    public static AudioClipView of(AudioClip clip) {
        return new AudioClipView(
                clip.getId(),
                clip.getBarTime(),
                clip.getSeq(),
                clip.getDurationMs(),
                clip.getFileName(),
                clip.getSizeBytes(),
                clip.getCreatedAt() == null ? 0 : clip.getCreatedAt().getEpochSecond(),
                clip.getActor() == null ? null : clip.getActor().name().toLowerCase(Locale.ROOT));
    }
}
