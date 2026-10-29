package com.tradingviewj.server.entity.replay.instance;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.replay.annotation.Annotation;
import com.tradingviewj.server.entity.replay.market.Bar;
import com.tradingviewj.server.entity.replay.protocol.Member;
import com.tradingviewj.server.entity.replay.trade.ClosedLot;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.trade.Position;
import com.tradingviewj.server.entity.replay.trade.Trade;
import com.tradingviewj.server.entity.replay.voice.AudioClip;
import com.tradingviewj.server.entity.replay.voice.VoiceState;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 进入实例时下发的一次性全量快照（对应 Go 的 {@code replay.InstanceDTO}）。
 *
 * <p>**只包含已经揭示的 K 线**（bars 长度 = cursor + 1）：后面的行情还在服务端，
 * 否则「往后拖一下就能看见未来」。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class InstanceDTO {

    private String id;

    private String symbol;

    private String folder;

    private String anchor;

    private int cursor;

    /** 最小跳动，价格轴按它定小数位 */
    private double tick;

    /** 一个 tick 的美元价值（0 = 没配置，按每点 1 美元） */
    private double tickValue;

    private List<Bar> bars;

    /** 已加载但还没揭示的根数 */
    private int hiddenBars;

    /** 数据目录里还有没有更晚的日文件 */
    private boolean hasMore;

    private List<Annotation> annotations;

    private List<Order> orders;

    private List<Trade> trades;

    /** 逐批平仓记录（画布上那些虚线的数据源） */
    private List<ClosedLot> closedLots;

    private Position position;

    private double realizedPnl;

    private double unrealizedPnl;

    private String operator;

    private List<Member> members;

    private boolean bot;

    /** 让「刷新页面之后」的界面还能说清机器人在跑什么；只存在内存里，不跟存档走 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private String botStrategy;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double botQuantity;

    private int seq;

    private List<Event> events;

    private List<String> notes;

    private String createdBy;

    private long createdAt;

    /** 当前这根 K 线上的语音讲解。**永远是对象，不是 null** */
    private VoiceState voice;

    /**
     * 整个实例的语音清单（按 K 线时间排序）。
     *
     * <p>为什么单独发一份：voice 只装当前这根，但复盘只能往前走、不能倒回——
     * 离开一根之后就再也回不去了，没有全量清单，早先几根上讲的语音会变成「听得见总数、点不到」。
     */
    private List<AudioClip> voiceAll;
}
