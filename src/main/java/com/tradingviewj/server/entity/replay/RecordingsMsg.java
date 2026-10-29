package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 交易记录列表（对应 Go 的 {@code replay.RecordingsMsg}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class RecordingsMsg {

    private String type;

    /** 记录目录；空 = 没配置，记录只活在内存里 */
    private String dir;

    private List<RecordingSummary> recordings;
}
