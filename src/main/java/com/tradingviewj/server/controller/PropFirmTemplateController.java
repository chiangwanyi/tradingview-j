package com.tradingviewj.server.controller;

import com.tradingviewj.server.api.R;
import com.tradingviewj.server.dto.PropFirmTemplateRequest;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.service.PropFirmTemplateService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * PF 模板接口：{@code /api/pf-templates}。
 *
 * <p>只做转发：这条链路上没有任何计算，规则是否自洽由 {@link PropFirmTemplate#define} 回答，
 * 模板在不在由 service 回答。多写一层「顺手校验」只会让同一条规则有两个落点。
 *
 * <p>返回的是实体（不是 DTO）：模板的字段集就是它的 API 契约，没有哪一列是「库里的私事」。
 * 代价是 {@code application.yml} 的 {@code non_null} 会把取值为 null 的字段整个省掉 ——
 * 前端读到的是 {@code undefined}，所以 {@code rules.ts} 里一律用 {@code == null} 判断。
 */
@Tag(name = "PF 模板", description = "考核规则模板的增删改查；删模板不影响已开出的账户")
@RestController
@RequestMapping("/api/pf-templates")
@RequiredArgsConstructor
public class PropFirmTemplateController {

    private final PropFirmTemplateService templates;

    @Operation(summary = "全部模板", description = "按创建时间升序，前端卡片列表的顺序。")
    @GetMapping
    public List<PropFirmTemplate> list() {
        return templates.list();
    }

    @Operation(summary = "取一套模板", description = "模板不存在时返回业务错误码 10000。")
    @GetMapping("/{id}")
    public PropFirmTemplate get(@PathVariable String id) {
        return templates.get(id);
    }

    @Operation(
            summary = "新建一套模板",
            description = """
                    stage 必填。规则自洽性由实体校验：EVAL 必须有盈利目标，FUNDED 不能有；
                    违反这两条建不出来（返回 400 + 中文原因）。
                    nextTemplateId 可以留空 = 这条链到此为止。
                    """)
    @PostMapping
    public PropFirmTemplate create(@RequestBody PropFirmTemplateRequest body) {
        return templates.create(body.name(), body.description(), body.stage(),
                body.nextTemplateId(), body.rules());
    }

    @Operation(
            summary = "改一套模板",
            description = """
                    就地改名字、说明、下一套、规则。请求体里的 stage 被忽略 ——
                    阶段是链上的一环，改它会让已有账户的升级路径静默错位，要换阶段请新建。
                    """)
    @PutMapping("/{id}")
    public PropFirmTemplate update(@PathVariable String id, @RequestBody PropFirmTemplateRequest body) {
        return templates.update(id, body.name(), body.description(), body.nextTemplateId(), body.rules());
    }

    @Operation(
            summary = "删一套模板",
            description = "幂等：删一个已经不存在的模板也返回成功。只删这一行，已开出的账户不受影响。")
    @DeleteMapping("/{id}")
    public R<Void> delete(@PathVariable String id) {
        templates.delete(id);
        return R.ok();
    }
}
