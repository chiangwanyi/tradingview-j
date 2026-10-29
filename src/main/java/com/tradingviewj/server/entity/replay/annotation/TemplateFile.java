package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * replay-templates.json 的磁盘形态（对应 Go 的 {@code replay.templateFile}）。
 *
 * <p>nextId 必须存：不存它会和旧模板撞号（tpl-3 撞 tpl-3）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class TemplateFile {

    private int version;

    private int nextId;

    private List<Template> templates;
}
