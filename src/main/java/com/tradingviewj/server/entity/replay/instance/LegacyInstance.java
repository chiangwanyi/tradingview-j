package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.annotation.Annotation;
import com.tradingviewj.server.entity.replay.market.Bar;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.trade.Position;
import com.tradingviewj.server.entity.replay.trade.Trade;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * v1 单文件存档里的实例（对应 Go 的 {@code replay.legacyInstance}）。
 *
 * <p>只用于迁移：读到它就把数据摊进新的文件夹结构。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class LegacyInstance {

    private String id;

    private String symbol;

    private String folder;

    private double tick;

    private String anchor;

    private int cursor;

    private List<Bar> bars;

    private List<Annotation> annotations;

    private List<Order> orders;

    private List<Trade> trades;

    private Position position;

    private double realizedPnl;

    private String createdBy;

    private long createdAt;

    private int seq;

    private List<Event> events;

    private List<String> notes;

    private int seqAnnotation;

    private int seqOrder;

    private int seqTrade;
}
