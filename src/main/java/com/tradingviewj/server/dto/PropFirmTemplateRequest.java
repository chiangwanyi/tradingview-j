package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;

/**
 * 建/改模板的请求体。
 *
 * <p>{@link #stage} 只在新建时有意义：{@code update} 时会被忽略（阶段在链上，建出来不能改）。
 *
 * <p>规则直接用 {@link PropFirmRules}，不另写一份镜像 —— 它的字段名就是前端 {@code rules.ts}
 * 对着的那一套，镜像一份出来只会立刻和实体漂移（本包 {@code package-info} 里「不 import entity」
 * 的理想状态，在这里为这条让步）。
 *
 * <p>这里的 {@code null} 是有意义的取值：FUNDED 的 {@code profitTarget} 就该是 {@code null}。
 */
public record PropFirmTemplateRequest(String name, String description, PropFirmStage stage,
                                      String nextTemplateId, PropFirmRules rules) {
}
