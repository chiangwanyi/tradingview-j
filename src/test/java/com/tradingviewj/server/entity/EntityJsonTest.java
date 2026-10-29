package com.tradingviewj.server.entity;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.entity.replay.annotation.Annotation;
import com.tradingviewj.server.entity.replay.annotation.AnnotationKind;
import com.tradingviewj.server.entity.replay.annotation.DashStyle;
import com.tradingviewj.server.entity.replay.market.Bar;
import com.tradingviewj.server.entity.replay.market.Point;
import com.tradingviewj.server.entity.replay.recording.Recording;
import com.tradingviewj.server.entity.replay.recording.RecordingBase;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.trade.OrderKind;
import com.tradingviewj.server.entity.replay.trade.OrderSide;
import com.tradingviewj.server.entity.replay.trade.OrderStatus;
import com.tradingviewj.server.entity.replay.trade.Position;
import com.tradingviewj.server.entity.replay.trade.PositionSide;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.json.JsonTest;

/**
 * 钉住线上 JSON 的形状：这些类是为了接手 Go 的下行协议，字段名/省略规则一旦和 Go 不同，
 * 前端就会收到读不动的消息。
 */
@JsonTest
class EntityJsonTest {

    @Autowired
    private ObjectMapper mapper;

    @Test
    void barKeepsEma20AsNull() throws Exception {
        Bar bar = Bar.builder().time("2026-10-16 12:00").close(7076.14).build();
        Map<String, Object> json = mapper.readValue(mapper.writeValueAsString(bar), Map.class);
        assertThat(json.keySet()).contains("time", "open", "high", "low", "close", "volume", "ema20");
        assertThat(json.get("ema20")).isNull();
    }

    @Test
    void pointOmitsZeroOffset() throws Exception {
        String zero = mapper.writeValueAsString(Point.builder().time("t").price(1).build());
        assertThat(zero).doesNotContain("offset");

        String moved = mapper.writeValueAsString(Point.builder().time("t").price(1).offset(40).build());
        assertThat(moved).contains("\"offset\":40");
    }

    @Test
    void orderOmitsEmptyFillButKeepsMinusOneIndex() throws Exception {
        Order open = Order.builder().id("o-1").status(OrderStatus.OPEN).filledIndex(-1).build();
        String json = mapper.writeValueAsString(open);
        assertThat(json).contains("\"filledIndex\":-1");
        assertThat(json).doesNotContain("filledAt", "fillPrice", "group");
    }

    /**
     * 枚举的线上取值必须和 Go 的一模一样（小写单词），而且能双向走通：
     * 只测序列化的话，反序列化一个未知取值要等到读存档那天才会炸。
     */
    @Test
    void enumsKeepGoWireValuesBothWays() throws Exception {
        Order order = Order.builder()
                .id("o-2")
                .side(OrderSide.BUY)
                .kind(OrderKind.STOP)
                .status(OrderStatus.CANCELLED)
                .build();
        String json = mapper.writeValueAsString(order);
        assertThat(json).contains("\"side\":\"buy\"", "\"kind\":\"stop\"", "\"status\":\"cancelled\"");

        Order back = mapper.readValue(json, Order.class);
        assertThat(back.getSide()).isEqualTo(OrderSide.BUY);
        assertThat(back.getKind()).isEqualTo(OrderKind.STOP);
        assertThat(back.getStatus()).isEqualTo(OrderStatus.CANCELLED);

        // 磁盘上真实存在的老存档片段：annotation 的 kind/dash、position 的 side
        Annotation ann = mapper.readValue(
                "{\"id\":\"ann-1\",\"kind\":\"ray\",\"style\":{\"dash\":\"dashed\"}}", Annotation.class);
        assertThat(ann.getKind()).isEqualTo(AnnotationKind.RAY);
        assertThat(ann.getStyle().getDash()).isEqualTo(DashStyle.DASHED);

        Position flat = mapper.readValue("{\"quantity\":0,\"side\":\"flat\"}", Position.class);
        assertThat(flat.getSide()).isEqualTo(PositionSide.FLAT);
    }

    @Test
    void recordingFlattensSummaryFields() throws Exception {
        Recording rec = Recording.builder()
                .id("rec-1")
                .stepCount(12)
                .format(2)
                .base(RecordingBase.builder().cursor(3).barTimes(List.of("t1")).build())
                .steps(List.of())
                .build();
        Map<String, Object> json = mapper.readValue(mapper.writeValueAsString(rec), Map.class);
        // Go 是内嵌结构，摘要字段与记录字段在同一个对象里；Java 用继承表达
        assertThat(json.keySet()).contains("id", "stepCount", "format", "base", "steps");
    }
}
