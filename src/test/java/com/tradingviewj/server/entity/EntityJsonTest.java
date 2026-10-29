package com.tradingviewj.server.entity;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.entity.replay.market.Bar;
import com.tradingviewj.server.entity.replay.trade.Order;
import com.tradingviewj.server.entity.replay.market.Point;
import com.tradingviewj.server.entity.replay.recording.Recording;
import com.tradingviewj.server.entity.replay.recording.RecordingBase;
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
        Order open = Order.builder().id("o-1").status("open").filledIndex(-1).build();
        String json = mapper.writeValueAsString(open);
        assertThat(json).contains("\"filledIndex\":-1");
        assertThat(json).doesNotContain("filledAt", "fillPrice", "group");
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
