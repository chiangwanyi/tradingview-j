package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.drawing.AnnotationTemplate;

import java.util.Locale;

/**
 * 一条样式模板<strong>在协议里</strong>的样子（前端 {@code webapp/src/types/protocol.ts}
 * 的 {@code Template}）。
 *
 * <p>{@link #style} 是**解析过的对象**，不是实体里那块 JSON 字符串 ——
 * 前端不该拿到一个字符串再自己 {@code JSON.parse}：那样「样式长什么样」就有了
 * 两处实现（后端 sanitize 时一处、前端解析时一处），而两份迟早对不上。
 *
 * @param createdAt Unix 毫秒。前端只拿它排 chip 的先后（新的排在后面）。
 */
public record TemplateView(
        String id,
        String kind,
        String name,
        AnnotationStyleView style,
        long createdAt
) {

    public static TemplateView of(AnnotationTemplate template, AnnotationStyleView style) {
        return new TemplateView(
                template.getId(),
                template.getKind() == null ? null : template.getKind().name().toLowerCase(Locale.ROOT),
                template.getName(),
                style,
                template.getCreatedAt() == null ? 0L : template.getCreatedAt().toEpochMilli());
    }
}
