package com.tradingviewj.server.ws;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.tradingviewj.server.dto.AnnotationStyleView;

/**
 * 一条「存模板」命令（{@code template_save}）。
 *
 * <p>{@code id} 为空 = 新建，由后端分配；有值 = 就地覆盖那条。
 *
 * <p>和 {@link WsRequest} 一样用 {@code ignoreUnknown}：前端多发一个字段（比如以后加个
 * {@code remark}）不该让整条命令变成「看不懂」。
 *
 * <p><strong>不接收 {@code createdAt}</strong>：时间由后端按落库那一刻算。
 * 让前端给的话，「新建」和「覆盖」两条路径就得各自决定「创建时间要不要跟着改」，
 * 而这个决定只有一处（{@code AnnotationTemplateService#save}）才说得清。
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public record TemplateCommand(
        String id,
        /** trend / ray / rect / arrow / fib */
        String kind,
        String name,
        AnnotationStyleView style
) {
}
