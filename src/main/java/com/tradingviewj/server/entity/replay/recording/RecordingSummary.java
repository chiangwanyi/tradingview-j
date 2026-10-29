package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.trade.TradeStats;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.experimental.SuperBuilder;

/**
 * 一条交易记录的摘要（对应 Go 的 {@code replay.RecordingSummary}），列表页用。
 *
 * <p>计数字段带 Count 后缀是为了不和 {@link Recording#getSteps()} 撞 JSON 名——
 * 一旦撞了，Go 的内嵌字段会被外层同名字段盖掉，列表里的「12 步」会变成一段数组。
 */
@Data
@SuperBuilder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class RecordingSummary {

    private String id;

    private String name;

    private String symbol;

    private String folder;

    /** 第 1 步（开仓前那一屏）的 K 线时间 */
    private String anchor;

    /** 最后一步那根 K 线的时间 */
    private String endTime;

    /** 这笔交易发生在哪个实例上；**只用于追溯和显示**，实例删掉之后会指向不存在的 id */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String instanceId;

    /** 共几步（含第 1 步；语音步也算） */
    private int stepCount;

    /** 共几条语音 */
    private int audioCount;

    /** 共多少个改盘面的操作（不含语音步） */
    private int eventCount;

    /** 推进了几根 K 线（含起点那根） */
    private int bars;

    /** 目录占用（JSON + 音频） */
    private long size;

    private String createdBy;

    private long createdAt;

    /** 收尾时间；持仓中还开着时等于最后一次写盘时间 */
    private long endedAt;

    private TradeStats stats;

    /** human / bot：来源在**开仓那一刻**定下来存进记录，后面的筛选才和回放里看到的一致 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private RecordingSource source;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String sourceId;

    /** 给人看的细分：真人 = 操作者名字；机器人 = 策略名 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String sourceName;

    /** 用户打的标签，顺序即打标签的顺序 */
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    private List<RecordTag> tags;
}
