package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一个工具下面的一套样式预设（对应 Go 的 {@code replay.Template}）。
 *
 * <p>模板是**全局的**（不属于某个实例）：换个标的复盘，还是那几条常用的线。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class Template {

    private String id;

    /** 这个模板属于哪种图形；它和样式一起决定画出来的样子 */
    private AnnotationKind kind;

    private String name;

    private AnnotationStyle style;

    private long createdAt;
}
