package com.tradingviewj.server.service;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.dto.AnnotationStyleView;
import com.tradingviewj.server.dto.TemplateView;
import com.tradingviewj.server.entity.drawing.AnnotationTemplate;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AnnotationTemplateRepository;
import com.tradingviewj.server.ws.TemplateCommand;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.ArrayList;
import java.util.List;

/**
 * 绘图样式模板的读写。模板是<strong>全局的一份习惯</strong>，不属于账户、不属于标的。
 *
 * <h2>为什么这张表从来没有「重置成出厂」这条路径</h2>
 *
 * <p>出厂样式由前端 {@code defaultStyle()} 兜底 —— 表里一条都没有的时候，
 * 界面显示「没有模板，用出厂样式」，画出来的线和有一条「默认」模板时一模一样。
 * 所以不需要在首次启动时往库里塞 5 条预置：塞了之后一旦用户把它们删光，
 * 重启又会冒出来，要解决就得再加一个「已初始化」标记 —— 一个纯粹为了兜底而生的状态。
 *
 * <h2>为什么删除一条不存在的模板是错误</h2>
 *
 * <p>两个标签页各开一个交易页时会发生这种情况：A 删了「金色实线」，B 的界面上还挂着它，
 * B 再点一次删除。静默成功会让 B 以为「删了」，其实删的是自己界面上那份已经不存在的副本。
 * 报错（「可能已被别人删掉」）是唯一能让人合理解释「列表为什么和刚才不一样」的说法。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class AnnotationTemplateService {

    private final AnnotationTemplateRepository templates;
    private final ObjectMapper json;

    /** 全量，按创建时间升序。每个工具几条，整份列表小到可以直接广播。 */
    @Transactional(readOnly = true)
    public List<TemplateView> list() {
        List<TemplateView> out = new ArrayList<>();
        for (AnnotationTemplate template : templates.findAllByOrderByCreatedAtAscIdAsc()) {
            out.add(TemplateView.of(template, readStyle(template)));
        }
        return out;
    }

    /**
     * 新建或就地覆盖。{@code id} 为空 = 新建。
     *
     * <p>覆盖时<strong>不动 {@code kind} 和 {@code createdAt}</strong>：前者换了等于变成
     * 另一个工具下的模板（要换就新建一条），后者决定 chip 的显示顺序 ——
     * 改个颜色不该让它跳到列表末尾。
     *
     * @throws BusinessException kind 不合法 / 数量超上限 / id 指向一个已经不存在的模板
     */
    @Transactional
    public TemplateView save(TemplateCommand input) {
        if (input == null) {
            throw new BusinessException("缺少 template");
        }
        Drawing.Kind kind = AnnotationStyleRules.kindOf(input.kind());
        AnnotationStyleView style = AnnotationStyleRules.sanitize(kind, input.style());
        String name = AnnotationStyleRules.name(input.name(), kind);
        String styleJson = writeStyle(style);

        String id = input.id() == null ? "" : input.id().trim();
        if (id.isEmpty()) {
            long count = templates.countByKind(kind);
            if (count >= AnnotationStyleRules.MAX_TEMPLATES_PER_KIND) {
                throw new BusinessException(AnnotationStyleRules.wire(kind) + " 的模板已经有 "
                        + AnnotationStyleRules.MAX_TEMPLATES_PER_KIND + " 个了：先删掉几个再新建");
            }
            AnnotationTemplate created = templates.save(
                    AnnotationTemplate.of(kind, name, styleJson, "human"));
            log.debug("新建绘图模板 {}（{} / {}）", created.getId(), created.getKind(), created.getName());
            return TemplateView.of(created, style);
        }

        AnnotationTemplate existing = templates.findById(id)
                .orElseThrow(() -> new BusinessException("没有这个模板：" + id + "（可能已被删掉）"));
        if (existing.getKind() != kind) {
            // 换工具 = 换一套模板。允许就地改会让它在另一个工具下面凭空冒出来，
            // 而原来那个工具下面少一条 —— 两个界面上都找不到「我改的那条」。
            throw new BusinessException("不能把模板从「" + AnnotationStyleRules.wire(existing.getKind())
                    + "」改成「" + AnnotationStyleRules.wire(kind) + "」：换个工具请新建一条");
        }
        existing.update(name, styleJson);
        log.debug("改绘图模板 {} → {}", existing.getId(), name);
        return TemplateView.of(existing, style);
    }

    /**
     * 删掉一条。已经画在图上的图形<strong>不受影响</strong> ——
     * 图形存的是画下去那一刻的样式快照，不是对模板的引用。
     *
     * @throws BusinessException 没有这条模板
     */
    @Transactional
    public void delete(String id) {
        String target = id == null ? "" : id.trim();
        if (target.isEmpty()) {
            throw new BusinessException("缺少 templateId");
        }
        if (!templates.existsById(target)) {
            throw new BusinessException("没有这个模板：" + target + "（可能已被删掉）");
        }
        templates.deleteById(target);
        log.debug("删除绘图模板 {}", target);
    }

    /**
     * 库里那块 JSON → 协议对象。
     *
     * <p>读不出来<strong>不让整份列表失败</strong>：一条坏行（手改过库、升级时字段改名）
     * 不该让用户看见「模板全没了」。退到出厂样式，界面照样能用，日志里留一句给排查用。
     */
    private AnnotationStyleView readStyle(AnnotationTemplate template) {
        try {
            return json.readValue(template.getStyleJson(), AnnotationStyleView.class);
        } catch (JsonProcessingException | IllegalArgumentException e) {
            log.warn("模板 {} 的样式读不出来（{}），这条按出厂样式显示", template.getId(), e.getMessage());
            return null; // 前端 resolveStyle 会补齐
        }
    }

    private String writeStyle(AnnotationStyleView style) {
        try {
            return json.writeValueAsString(style);
        } catch (JsonProcessingException e) {
            // 走到这里说明 DTO 里出现了画不出来的东西，属于代码缺陷，不该写一条坏行进去
            throw new IllegalStateException("模板样式无法序列化", e);
        }
    }
}
