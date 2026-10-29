package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个实例落盘后的全部内容（对应 Go 的 {@code replay.instanceFiles}），读写共用。
 *
 * <p>拆成字段而不是一个大结构体，是为了每类数据一个文件、只读需要的那个。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class InstanceFiles {

    private InstanceMeta meta;

    private List<Bar> bars;

    private List<Annotation> annotations;

    private List<Order> orders;

    private List<Trade> trades;

    private List<Event> events;

    /** 语音的元数据。音频不在这里——它是 voice/ 下的 wav，JSON 只记「哪一段、多长、哪个文件」 */
    private List<AudioClip> voice;
}
