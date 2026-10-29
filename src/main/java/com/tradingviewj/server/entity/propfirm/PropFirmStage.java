package com.tradingviewj.server.entity.propfirm;

/**
 * 账户所处的阶段。生命周期是<strong>单向</strong>的：{@code EVAL → FUNDED → PAYOUT}，不回头。
 *
 * <p>三个阶段不是"同一个账户的三种状态"，而是<strong>三套不同的考核</strong>：
 * 规则不同（目标/一致性/手数分档全变）、余额基准不同（升级时重置回账户规模）、
 * 统计范围不同（EVAL 的盈利不带进 FUNDED）。
 *
 * <p>所以阶段切换在数据上是「换一份规则快照 + 换一个余额基准 + 在流水里记一行」，
 * 不是「改一个字段」。这个类只描述"有哪几个阶段"，怎么切在 {@code PropFirmAccountService#promote}。
 */
public enum PropFirmStage {

    /** 考试阶段：有盈利目标、查一致性。 */
    EVAL,

    /** 出金号阶段：无目标、不查一致性，但要攒够"盈利日"。 */
    FUNDED,

    /**
     * 出金申请及处理阶段 = <strong>结束</strong>。
     *
     * <p>进入这个阶段之后账户<strong>不再接受下单</strong>（{@code requireTradable} 会拒绝），
     * 但看盘动作（画图、录音、开复盘）仍然允许 —— 用户可能想在收工之后回来标注一遍。
     * 「不再交易」和「这行数据作废」是两件事，所以账户的 {@code status} 仍然是 ACTIVE。
     */
    PAYOUT;

    /** 这个阶段还能不能交易。PAYOUT 不能 —— 它是终点，不是新的起点。 */
    public boolean tradable() {
        return this != PAYOUT;
    }
}
