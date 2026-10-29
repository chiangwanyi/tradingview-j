package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 实例列表里的一行（对应 Go 的 {@code replay.InstanceSummary}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class InstanceSummary {

    private String id;

    private String symbol;

    /** 锚点 K 线的时间 */
    private String anchor;

    private int cursor;

    private int loadedBars;

    private int members;

    /** 当前操作者的名字；空 = 没人接管 */
    private String operator;

    private boolean bot;

    /** 机器人正在跑的策略 id；空 = 随机演示机器人 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String botStrategy;

    private String createdBy;

    private long createdAt;

    /** 实时盈亏摘要：列表上就能看到这个实例做到哪儿了 */
    private double realizedPnl;

    private int trades;
}
