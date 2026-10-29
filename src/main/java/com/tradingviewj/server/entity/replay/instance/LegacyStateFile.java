package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * v1 的 replay-instances.json：所有实例都在一个文件里（对应 Go 的 {@code replay.legacyStateFile}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class LegacyStateFile {

    private int version;

    private long savedAt;

    private int nextInstance;

    private List<LegacyInstance> instances;
}
