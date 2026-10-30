package com.tradingviewj.server.dto;

/**
 * 开账户 / 改名的请求体。
 *
 * <p>建账户同时要三样东西：叫什么名、按哪套模板的规则开、<strong>做哪个标的</strong>。
 * {@code symbolId} <strong>只在开账户时读</strong>，而且是必填 —— 账户一旦建出来，
 * 标的就固定了，「换个标的」等于「重开一个账户」。
 *
 * <p>改名只读 {@code name}，{@code templateId} 和 {@code symbolId} 都被忽略：
 * 换模板等于换一整套规则和余额基准、换标的等于换一整条时间轴，两者都是「重开一个账户」，
 * 不是「改一个字段」（阶段链见 {@code PropFirmTemplate#nextTemplateId}）。
 */
public record PropFirmAccountRequest(String name, String templateId, String symbolId) {
}
