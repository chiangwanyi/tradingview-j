package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Embedded;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * Prop Firm 模板：<strong>一个阶段的</strong>规则预设。
 *
 * <p><strong>职责边界</strong>：它只是一张「价目表」—— 描述某家自营盘的某档考核长什么样。
 * 它不持有任何账户、不累计任何盈亏、不参与任何判定。删掉模板不影响已经开出来的账户
 * （账户存的是规则副本，见 {@link PropFirmRules} 的说明）。
 *
 * <p><strong>一套考核 = 一条链，不是一个模板。</strong> LucidFlex 50K 至少要两条：
 * 「LucidFlex 50K EVAL」{@code --next_template_id-->} 「LucidFlex 50K FUNDED」。
 * 账户在 EVAL 达标时，按 {@link #nextTemplateId} 找到下一套规则换上去。
 * 之所以不把两个阶段塞进一个模板：它们的字段集根本不同（EVAL 有目标没盈利日，
 * FUNDED 有盈利日没目标），塞在一起会得到一半字段永远为 null 的「配置项」。
 *
 * <p>{@code nextTemplateId} 是自引用，建表时无法同时插入，所以它由后续一次更新设置。
 *
 * <p>模板<strong>不做版本化</strong>。看似需要，其实不需要：真正必须稳定的快照在账户上，
 * 模板随便改，改完新开的账户就用新规则。给模板做版本号只会带来「我现在用的是第几版」
 * 这个没人想回答的问题。
 */
@Getter
@Entity
@Table(name = "prop_firm_template")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class PropFirmTemplate extends BaseEntity {

    @Setter
    @Column(nullable = false)
    private String name;

    @Setter
    private String description;

    /** 用这套模板开出来的账户，直接处于这个阶段。 */
    @Column(nullable = false)
    @Enumerated(EnumType.STRING)
    private PropFirmStage stage;

    /** 达标之后接哪套模板。{@code null} = 升到尽头（没有再下一套）。 */
    @Setter
    private String nextTemplateId;

    @Setter
    @Embedded
    private PropFirmRules rules;

    /**
     * 定义一套考核规则。
     *
     * <p>这里做的是<strong>阶段与字段的自洽校验</strong>，不是业务计算：
     *
     * <ul>
     *   <li>EVAL 必须有盈利目标 —— 没有目标就不是考试；</li>
     *   <li>FUNDED 不能有盈利目标 —— 出金号如果还有通关目标，那它其实是第二场考试。
     *       这一类错误（把 EVAL 的模板误标成 FUNDED）会让账户永远升不了级，
     *       却在界面上看起来完全正常。宁可建不出来。</li>
     * </ul>
     *
     * @throws IllegalArgumentException 缺名字、缺规模、或缺阶段必需的规则
     */
    public static PropFirmTemplate define(String name, String description, PropFirmStage stage,
                                          String nextTemplateId, PropFirmRules rules) {
        validate(name, stage, rules);

        PropFirmTemplate template = new PropFirmTemplate();
        template.name = name;
        template.description = description;
        template.stage = stage;
        template.nextTemplateId = nextTemplateId;
        template.rules = rules;
        return template;
    }

    /**
     * 就地改一套模板：名字、说明、下一套、规则。
     *
     * <p><strong>阶段不改</strong> —— 它在链上，改一下就意味着已经开出去的账户升级路径变了。
     * 校验复用 {@link #define} 那条路径：更新时把规则改成和阶段矛盾（例如 FUNDED 还带盈利目标），
     * 一样建不出来，不会出现「建的时候自洽、改完不自洽」的模板。
     *
     * @throws IllegalArgumentException 违反 {@link #define} 的任一条件
     */
    public void redefine(String name, String description, String nextTemplateId, PropFirmRules rules) {
        validate(name, stage, rules);
        this.name = name;
        this.description = description;
        this.nextTemplateId = nextTemplateId;
        this.rules = rules;
    }

    /** 阶段与字段的自洽校验，建和改共用。 */
    private static void validate(String name, PropFirmStage stage, PropFirmRules rules) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("模板必须有名字，收到: " + name);
        }
        if (stage == null) {
            throw new IllegalArgumentException("模板必须指定阶段，收到: null");
        }
        if (rules == null) {
            throw new IllegalArgumentException("模板必须带规则，收到: null");
        }
        if (rules.getAccountSize() == null || rules.getMaxDrawdown() == null) {
            throw new IllegalArgumentException("规则缺账户规模或最大回撤，开不出账户: " + name);
        }
        if (stage == PropFirmStage.EVAL && rules.getProfitTarget() == null) {
            throw new IllegalArgumentException("EVAL 阶段必须有盈利目标: " + name);
        }
        if (stage == PropFirmStage.FUNDED && rules.getProfitTarget() != null) {
            throw new IllegalArgumentException("FUNDED 阶段不能有盈利目标（那是 EVAL 的规则）: " + name);
        }
    }
}
