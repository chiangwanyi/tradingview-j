package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 删掉一个图形（对应 Go 的 {@code replay.AnnotationDeletedPayload}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class AnnotationDeletedPayload {

    private String annotationId;
}
