package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 索引里的一行（对应 Go 的 {@code replay.instanceRef}）。
 *
 * <p>dir 允许为空（= 用 id），留给以后改目录名的余地。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class InstanceRef {

    private String id;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String dir;
}
