package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.protocol.You;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 进入实例时的一次性全量（对应 Go 的 {@code replay.InstanceStateMsg}）。
 *
 * <p>注意：join_instance 之后收到的第一条是 control_changed，不是这条。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class InstanceStateMsg {

    private String type;

    private You you;

    private InstanceDTO instance;
}
