package com.tradingviewj.server.entity.replay.annotation;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 模板全量列表（对应 Go 的 {@code replay.TemplatesMsg}）。
 *
 * <p>模板很少（每个工具几条），所以任何一次改动都直接把整份列表广播给所有人，
 * 前端不用维护增量合并。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class TemplatesMsg {

    private String type;

    private List<Template> templates;

    /** 这次刚存下来的是哪一条：新建时 id 由后端分配，前端靠它把刚建好的那条立刻选上 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String lastSavedId;
}
