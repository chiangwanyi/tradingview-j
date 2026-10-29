package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 交易记录目录的索引（对应 Go 的 {@code replay.recordingsIndex}），列表走它、不读每条记录。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class RecordingsIndex {

    private int version;

    private long savedAt;

    private List<RecordingSummary> items;
}
