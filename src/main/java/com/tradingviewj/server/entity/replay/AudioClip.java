package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一段挂在某根 K 线上的录音（对应 Go 的 {@code replay.AudioClip}）。
 *
 * <p>存储键是 **K 线时间**，不是下标、不是时间戳：回放时按时间找挂载点，
 * 录像内数据被重排也不会错位。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class AudioClip {

    private String id;

    /** 录音那一步的步号（v0.15.0 起录音自己占一步），列表里显示「第几步」用 */
    private int step;

    private String time;

    private int durationMs;

    /** 录像目录里的文件名（相对路径），音频本身不进 JSON */
    private String file;

    private long size;

    private long createdAt;

    /** 谁录的（human / bot / system）；旧录像里没有这个字段，是空串 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String by;
}
