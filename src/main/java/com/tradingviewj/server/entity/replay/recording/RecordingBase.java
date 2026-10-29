package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.annotation.Annotation;
import com.tradingviewj.server.entity.replay.market.Bar;
import com.tradingviewj.server.entity.replay.trade.ClosedLot;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.trade.Position;
import com.tradingviewj.server.entity.replay.trade.Trade;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 开仓**之前**那一刻的盘面（对应 Go 的 {@code replay.RecordingBase}）。
 *
 * <p>关键在「之前」：第 2 步就是开仓成交，回放从空仓开始演——这样「怎么进的场」才看得清楚。
 * 如果 base 取成开仓之后，回放的第一帧就已经带着仓位，入场那一下永远不会出现。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class RecordingBase {

    private int cursor;

    /** 内存里的完整盘面；落盘时会被换成 barTimes（OHLC 本来就在数据目录的日文件里） */
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    private List<Bar> bars;

    /**
     * 只在 recording.json 里出现：盘面上那几根 K 线的时间，按顺序排。
     *
     * <p>它是**内容清单**而不只是首尾范围：数据目录被清理过时能看出缺了哪几根，
     * 而不是悄悄把中间的缺口抹掉（那样游标、图形、语音会集体错位）。
     */
    @JsonInclude(JsonInclude.Include.NON_EMPTY)
    private List<String> barTimes;

    private List<Annotation> annotations;

    private List<Order> orders;

    private List<Trade> trades;

    private List<ClosedLot> closedLots;

    private Position position;

    private double realizedPnl;

    private double unrealizedPnl;
}
