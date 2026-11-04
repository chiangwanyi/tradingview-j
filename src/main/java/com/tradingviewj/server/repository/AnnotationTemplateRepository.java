package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.drawing.AnnotationTemplate;
import com.tradingviewj.server.entity.drawing.Drawing;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/**
 * 模板<strong>一次全取</strong>（每个绘图工具几条，全量也就几十条）。
 *
 * <p>没有「按账户查」「按标的查」的方法，因为模板不属于任何一方 ——
 * 它是本机的长期习惯，换账户、换标的、删账户都不该影响它（见 {@link AnnotationTemplate}）。
 * 这也是它能一次读完的前提：整份列表小到值得全量广播。
 */
public interface AnnotationTemplateRepository extends JpaRepository<AnnotationTemplate, String> {

    /**
     * 全量，按<strong>创建时间升序</strong>。
     *
     * <p>{@code id} 作为第二排序键只是为了让「同一毫秒内建的两条」有个确定顺序 ——
     * 界面上 chip 的先后不该在两次刷新之间自己换位置。
     * 刻意**不按 id 字符串排**：{@code tpl-10} 会跑到 {@code tpl-2} 前面。
     */
    List<AnnotationTemplate> findAllByOrderByCreatedAtAscIdAsc();

    /** 单个工具下的模板条数，用来卡上限（列表是全量广播的，攒到几百条会拖慢每次操作）。 */
    long countByKind(Drawing.Kind kind);
}
