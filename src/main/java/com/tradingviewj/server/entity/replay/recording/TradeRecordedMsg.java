package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「一笔交易刚刚收尾、记录已保存」的通知（对应 Go 的 {@code replay.TradeRecordedMsg}）。
 *
 * <p>它不是广播（不进实例的操作记录），而是一次性消息：交易记录属于独立的数据集。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class TradeRecordedMsg {

    private String type;

    private RecordingSummary recording;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String dir;

    /** 非空表示记录本身完整，只是没落到磁盘（没配目录 / 磁盘满） */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String warning;
}
