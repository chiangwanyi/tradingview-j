package com.tradingviewj.server.entity.propfirm;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Embedded;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * Prop Firm 模板：一套考核规则的<strong>预设</strong>。
 *
 * <p><strong>职责边界</strong>：它只是一张「价目表」—— 描述某家自营盘的某档考核长什么样。
 * 它不持有任何账户、不累计任何盈亏、不参与任何判定。删掉模板不影响已经开出来的账户
 * （账户存的是规则副本，见 {@link PropFirmRules} 的说明）。
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

    @Setter
    @Embedded
    private PropFirmRules rules;

    public static PropFirmTemplate define(String name, String description, PropFirmRules rules) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("模板必须有名字，收到: " + name);
        }
        PropFirmTemplate template = new PropFirmTemplate();
        template.name = name;
        template.description = description;
        template.rules = rules;
        return template;
    }
}
