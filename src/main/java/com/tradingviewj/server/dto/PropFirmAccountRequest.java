package com.tradingviewj.server.dto;

/**
 * 开账户 / 改名的请求体。
 *
 * <p>建账户同时要两样东西：叫什么名、按哪套模板的规则开。改名只读 {@code name}，
 * {@code templateId} 会被忽略 —— 换模板等于换一整套规则和余额基准，那是「重开一个账户」，
 * 不是「改一个字段」（阶段链见 {@code PropFirmTemplate#nextTemplateId}）。
 */
public record PropFirmAccountRequest(String name, String templateId) {
}
