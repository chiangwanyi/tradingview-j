package com.tradingviewj.server.entity.replay;

import java.util.List;
import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「回放末尾又加载了几根 K 线」的回复（对应 Go 的 {@code replay.RecordingExtendedMsg}）。
 *
 * <p>只带新增的那几步和摘要：几百根 K 线加几百步的记录重传一遍既慢又没必要。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class RecordingExtendedMsg {

    private String type;

    private String recordingId;

    private List<RecordingStep> steps;

    /** 保存之后的那一份（步数 / 根数 / 结束时间都变了），前端顺手刷新列表那一行 */
    private RecordingSummary recording;

    private boolean hasMore;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String warning;
}
