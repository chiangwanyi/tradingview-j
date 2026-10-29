package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 实例列表（对应 Go 的 {@code replay.InstancesMsg}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class InstancesMsg {

    private String type;

    private List<InstanceSummary> instances;
}
