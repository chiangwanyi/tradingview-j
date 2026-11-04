package com.tradingviewj.server.dto;

import java.math.BigDecimal;

/**
 * 账户<strong>推到某一根 K 线时</strong>的考核读数。快照和三条会动钱的广播都带它。
 *
 * <p>为什么它不是几个散字段而是一个小对象：余额、日终峰值、亏损线、权益这四个数
 * <strong>必须来自同一次重放</strong>。分开算就会出现「余额是这一根的、亏损线还是上一根定的」
 * 这种自相矛盾的一帧，而亏损差一点就是考核失败 —— 界面宁可少一个数字，也不能给出一组配不上的数。
 *
 * <h2>为什么亏损线由后端给，不让前端拿规则现算</h2>
 *
 * <p>它不是「峰值 − 最大回撤」这么简单：EOD 追踪还有一道<strong>冻结水位</strong>
 * （{@code trailingLockLevel}，LucidFlex 50K 是 52100）。峰值越过 52100 之后亏损线
 * 停在 50100，此后赚多少都不再上移。前端自己算就会把这条线一直往上抬 ——
 * 抬上去的是「以为还剩多少缓冲」，而真实缓冲比显示的小，这正是最容易让人满仓的一次判断。
 * 所以口径留在 {@code service.PropFirmSession#floorLine()} 一处，界面只排版。
 *
 * @param status         ACTIVE / FAILED（账户当前状态，失败时界面要停手）
 * @param balance        当前余额 = 本阶段起点余额 + 本阶段已实现净额
 * @param eodPeakBalance 本阶段「日终余额」历史最高，亏损线由它推出来
 * @param lossLine       当前亏损线（美元）。权益 ≤ 它即判失败
 * @param equity         权益 = 余额 + 持仓<strong>最不利浮盈亏</strong>（多单用 low、空单用 high）
 * @param frozen         已达成本阶段升级条件、等用户手动升级。此状态下账户不接受下单
 * @param hint           一句话说明：还差多少达标 / 冻结原因 / 判失败原因。没有就是 {@code null}
 */
public record PfView(
        String status,
        BigDecimal balance,
        BigDecimal eodPeakBalance,
        BigDecimal lossLine,
        BigDecimal equity,
        boolean frozen,
        String hint
) {
}
