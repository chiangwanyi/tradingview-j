package com.tradingviewj.server.entity.converter;

import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

import java.time.Instant;

/**
 * {@code Instant} ↔ Unix 毫秒（INTEGER）。
 *
 * <p>不用 Hibernate 默认的时间映射：SQLite 没有原生日期类型，默认映射会落到
 * 字符串格式上，比较/排序就变成字符串比较（"2026-1-2" 排在 "2026-10-1" 之后）。
 * 毫秒整数既小又能正确比较，和 Go 侧的 {@code int64} 语义也一致。
 *
 * <p>注意 <strong>K 线时间不走这里</strong>：K 线用的是 CSV 里那一列的原始明文
 * （{@code "2026-10-16 12:00"}），它是协议里的唯一标识，一旦转成时间戳就再也对不上了。
 */
@Converter(autoApply = true)
public class InstantEpochConverter implements AttributeConverter<Instant, Long> {

    @Override
    public Long convertToDatabaseColumn(Instant value) {
        return value == null ? null : value.toEpochMilli();
    }

    @Override
    public Instant convertToEntityAttribute(Long stored) {
        return stored == null ? null : Instant.ofEpochMilli(stored);
    }
}
