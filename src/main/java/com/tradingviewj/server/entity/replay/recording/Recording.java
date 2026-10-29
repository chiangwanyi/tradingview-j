package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.voice.AudioClip;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.EqualsAndHashCode;
import lombok.NoArgsConstructor;
import lombok.experimental.SuperBuilder;

/**
 * 一条交易记录的全部内容（对应 Go 的 {@code replay.Recording}）。
 *
 * <p>它**不是视频**，而是「开仓前那一屏 + 之后每一个操作的原始广播」：
 * 回放就是把 base 深拷贝一份，然后按顺序把事件重新「演」一遍——和实时界面用同一套
 * 状态更新逻辑，所以回放看到的画面和当时看到的必然一模一样。
 *
 * <p>Java 里用继承表达 Go 的内嵌：摘要字段平铺在同一个 JSON 对象里。
 */
@Data
@SuperBuilder
@NoArgsConstructor
@AllArgsConstructor
@EqualsAndHashCode(callSuper = true)
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class Recording extends RecordingSummary {

    /** 2 = 一步一个操作；1 = 旧的一步一根 K 线。两种格式**不迁移、不混读** */
    private int format;

    private double tick;

    private double tickValue;

    private RecordingBase base;

    private List<RecordingStep> steps;

    private List<AudioClip> audios;
}
