package com.tradingviewj.server.entity.strategy;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

/**
 * 策略注册项。
 *
 * <p><strong>职责边界</strong>：这张表只记录「这台机器上有哪些策略可选、默认参数是什么、
 * 开没开」。它<strong>不含任何算法</strong> —— 算法的实现是 {@code strategy} 包里
 * 实现 {@code TradingStrategy} 接口的 Java 类，{@link #code} 就是那个 bean 的名字。
 *
 * <p>为什么不在库里存代码或表达式：策略是编译期确定的（要写 Java、要测试、要 review），
 * 从库里加载代码意味着「改一下数据库就能改变下单逻辑」，任何一次误操作都能让机器人
 * 拿真钱去试。所以「有哪些策略」可以存，「策略长什么样」必须是代码。
 *
 * <p>Go 版的策略清单是硬编码在 {@code list_strategies} 里的一个数组，参数只能填不能存。
 * 拆成这张表之后，「启用了哪些、默认手数多少」才是可配置的。
 */
@Getter
@Entity
@Table(name = "strategy")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Strategy extends BaseEntity {

    /** 实现类的 bean 名，全库唯一。 */
    @Column(nullable = false, unique = true)
    private String code;

    @Setter
    @Column(nullable = false)
    private String name;

    /** 一句话说明（给启动面板显示）。 */
    @Setter
    private String summary;

    /** 默认参数的 JSON。 */
    @Setter
    private String paramsJson;

    @Setter
    @Column(nullable = false)
    private boolean enabled = true;

    public static Strategy register(String code, String name, String summary, String paramsJson) {
        Strategy strategy = new Strategy();
        strategy.code = code;
        strategy.name = name;
        strategy.summary = summary;
        strategy.paramsJson = paramsJson;
        return strategy;
    }
}
