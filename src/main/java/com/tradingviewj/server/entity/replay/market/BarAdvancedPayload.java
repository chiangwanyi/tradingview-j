package com.tradingviewj.server.entity.replay.market;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.trade.ClosedLot;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.trade.Position;
import com.tradingviewj.server.entity.replay.trade.Trade;
import com.tradingviewj.server.entity.replay.voice.VoiceState;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 推进一根 K 线之后的结果（对应 Go 的 {@code replay.BarAdvancedPayload}）。
 *
 * <p>订单/成交/持仓每次都发全量：一次复盘通常个位数，全量发送让前端不用维护增量合并。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class BarAdvancedPayload {

    private Bar bar;

    private int cursor;

    private int hiddenBars;

    private boolean hasMore;

    private List<Order> orders;

    private List<Trade> trades;

    private List<ClosedLot> closedLots;

    private Position position;

    private double realizedPnl;

    private double unrealizedPnl;

    /** 这一根 K 线触发成交的单（可能为空） */
    private List<Trade> fills;

    /** 换根之后的语音状态：清单一换，界面上的「本根 x/9」立刻跟着走（和有没有在录制无关） */
    private VoiceState voice;
}
