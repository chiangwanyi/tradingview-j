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
 *
 * @param year 只在标的这一年的日文件里抽起点（{@code yyyy}，如 {@code 2024}）。
 *             <strong>可以为空</strong>，空 = 全部年份完全随机 —— 这是老行为，
 *             年份只是一个可选筛子，同一年里抽哪一天仍然随机。
 *             改名时同样被忽略。
 */
public record PropFirmAccountRequest(String name, String templateId, String symbolId, Integer year) {
}
