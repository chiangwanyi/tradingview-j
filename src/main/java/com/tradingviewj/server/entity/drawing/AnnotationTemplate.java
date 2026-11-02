package com.tradingviewj.server.entity.drawing;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * 一个绘图工具的<strong>样式预设</strong>（趋势线 / 射线 / 矩形 / 箭头 / 斐波那契 各挂一组）。
 *
 * <p><strong>它不是图形，也不是图形的一部分</strong>：图形画下去那一刻把样式
 * <em>复制</em>进 {@link Drawing#getStyle()}，此后改模板、删模板都不影响已经画好的线。
 * 这条边界见 {@link DrawingStyle} 的说明。
 *
 * <p><strong>它不挂在任何东西上</strong>：没有 {@code symbolId}，没有 {@code accountId}。
 * 模板说的是「我习惯用什么颜色的线」，跟画在哪个品种、哪个账户无关 ——
 * 挂上去就会长出「同一条预设换个账户就没了」这种需要解释的行为。
 * 这和 {@link Drawing} 没有 {@code accountId} 是同一条边界，只是理由不同：
 * 图形依附 K 线，模板依附人。
 *
 * <p><strong>样式存一整块 JSON</strong>（{@link #styleJson}），不展平成列。
 * 与 {@code drawing} 表相反，因为：{@code drawing} 的样式是<strong>事实</strong>的一部分，
 * 会被按标的下查、被逐列读；模板的样式是纯<strong>偏好</strong>，全库没有一处会
 * {@code where color = ...}，唯一的读法就是「整块拿出来交给前端」。
 * 展平还得为斐波那契那四条水平线再开一张子表，而它在这里没有任何查询价值。
 */
@Getter
@Entity
@Table(name = "annotation_template")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AnnotationTemplate extends BaseEntity {

    /**
     * 这套模板属于哪个绘图工具。用 {@link Drawing.Kind} 而不是另立一套枚举 ——
     * 两者必须是同一个集合，分两份迟早出现「模板有第六种、图形没有」。
     */
    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Drawing.Kind kind;

    /** 显示名，界面上一排 chip 上写的就是它。 */
    @Setter
    @Column(nullable = false)
    private String name;

    /**
     * 整份 {@code AnnotationStyle} 的 JSON（color / width / dash / fill / fillOpacity / levels）。
     *
     * <p>存字符串而不是映射成对象：这列从不需要被查询或比较，
     * 而映射成 {@code @ElementCollection} 或子表只会把「一份配置」拆成「一堆行」。
     * 读写都由 {@code AnnotationTemplateService} 用同一个 ObjectMapper 负责。
     */
    @Setter
    @Column(name = "style_json", nullable = false)
    private String styleJson;

    /** 谁建的。模板是全局的，所以这里不是「算在谁头上」，只是「谁干的」。 */
    @Column(nullable = false, updatable = false)
    private String createdBy;

    /**
     * 建一条模板。
     *
     * @param kind     哪个工具（不能为空 —— 不属于任何工具的模板在界面上没有位置）
     * @param name     显示名
     * @param styleJson 整份样式的 JSON
     * @param createdBy 谁建的
     */
    public static AnnotationTemplate of(Drawing.Kind kind, String name, String styleJson, String createdBy) {
        if (kind == null) {
            throw new IllegalArgumentException("模板必须属于某个绘图工具");
        }
        AnnotationTemplate template = new AnnotationTemplate();
        template.kind = kind;
        template.name = name;
        template.styleJson = styleJson;
        template.createdBy = createdBy;
        return template;
    }

    /**
     * 就地改掉名字和样式。
     *
     * <p><strong>不改 {@code kind}</strong>：换工具等于「这是另一套模板了」——
     * 界面上按 kind 分组，换了它就会在另一个工具下面凭空冒出一条。
     * 调用方要用另一个工具的模板，走「新建」。
     */
    public void update(String name, String styleJson) {
        this.name = name;
        this.styleJson = styleJson;
    }
}
