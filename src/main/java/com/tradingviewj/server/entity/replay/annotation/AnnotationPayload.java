package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 新增/移动/改样式之后的图形（对应 Go 的 {@code replay.AnnotationPayload}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class AnnotationPayload {

    private Annotation annotation;
}
