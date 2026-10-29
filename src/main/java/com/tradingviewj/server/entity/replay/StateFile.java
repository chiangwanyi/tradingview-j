package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 实例存档目录的索引（对应 Go 的 {@code replay.stateFile}）。
 *
 * <p>nextInstance 必须存：不存它重启后新实例会和旧的撞号（ins-1 撞 ins-1，前端 join 到错的实例）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class StateFile {

    private int version;

    private long savedAt;

    private int nextInstance;

    private List<InstanceRef> instances;
}
