package com.tradingviewj.server.converter;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

import java.io.UncheckedIOException;
import java.util.List;

/**
 * {@code List<PositionTier>} ↔ 一列 JSON 文本。
 *
 * <p><strong>为什么用手数分档不用子表</strong>：分档是<strong>规则的一部分</strong>，
 * 和 {@code max_drawdown} 一样必须跟着账户复制一份快照（模板改了，我签的那份不能变）。
 * 做成子表就得写两套「模板的分档」和「账户的分档」以及它们之间的复制代码；
 * 做成 JSON 一列，复制就是一次字符串拷贝，快照语义天然成立。
 *
 * <p>代价是<b>查不了</b>「哪些模板允许 40 手」—— 本项目的用法里没有这种查询，
 * 真需要时读出来在内存里筛（一张表最多几条）。
 *
 * <p>Jackson 而不是拼字符串：{@link PositionTier} 是 record，序列化/反序列化由它做，
 * 将来加一个字段（比如"每档的日内最大亏损"）不用同时改读写两边。
 */
@Converter
public class PositionTiersConverter implements AttributeConverter<List<PositionTier>, String> {

    private static final ObjectMapper JSON = new ObjectMapper();
    private static final TypeReference<List<PositionTier>> TYPE = new TypeReference<>() {
    };

    @Override
    public String convertToDatabaseColumn(List<PositionTier> tiers) {
        if (tiers == null || tiers.isEmpty()) {
            // 空列表和"没有分档"是两件事，但落到库上都是"这条规则不限手数"，存 NULL 更省
            return null;
        }
        try {
            return JSON.writeValueAsString(tiers);
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new UncheckedIOException("手数分档无法序列化", e);
        }
    }

    @Override
    public List<PositionTier> convertToEntityAttribute(String stored) {
        if (stored == null || stored.isBlank()) {
            return List.of();
        }
        try {
            return List.copyOf(JSON.readValue(stored, TYPE));
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            // 坏档不静默退化成"不限手数"：那会让一个手数超限的账户看起来合法
            throw new IllegalStateException("手数分档 JSON 无法解析: " + stored, e);
        }
    }
}
