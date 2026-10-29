package com.tradingviewj.server.entity.replay.protocol;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「这个实例被删掉了」（对应 Go 的 {@code replay.DeletedMsg}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class DeletedMsg {

    private String type;

    private String instanceId;
}
