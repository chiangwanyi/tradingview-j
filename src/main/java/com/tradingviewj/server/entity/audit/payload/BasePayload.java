package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;

/**
 * 交易记录的<strong>第 0 步</strong>：开仓之前那一屏（{@link ReplayAction#BASE}）—— <strong>没有负载</strong>。
 *
 * <p>起点不是「用户做了什么」，而是「回放从这里开始」的定位点，所以它不带盘面快照：
 * 那一屏的 K 线、图形、挂单、成交、持仓由交易记录自己保存（{@code TradeRecord} 的
 * {@code startBarTime} + {@code startDayFile} 定位，回放时从数据目录重建）。
 * 把整份盘面塞进一条审计事件的 JSON 里，等于让「日志」承担「状态」，那条分界一破就收不回来了。
 *
 * <p>由 {@code AuditEvent#base} 写入；{@code TradeRecord.startSeq} 指着它。
 */
public record BasePayload() implements ReplayActionPayload {

    @Override
    public ReplayAction action() {
        return ReplayAction.BASE;
    }
}
