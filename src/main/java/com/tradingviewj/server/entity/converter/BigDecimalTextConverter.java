package com.tradingviewj.server.entity.converter;

import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

import java.math.BigDecimal;

/**
 * {@code BigDecimal} ↔ {@code TEXT}。
 *
 * <p>为什么必须存在：SQLite 没有 decimal 类型。列声明成 {@code DECIMAL(19,8)} 时，
 * SQLite 的 NUMERIC 亲和性会把它当 REAL 存 —— {@code 0.1 + 0.2} 那套二进制浮点误差
 * 就直接进库了。账目（余额、回撤、已实现盈亏）不能这么存。
 *
 * <p>所以凡是 BigDecimal 的列，DDL 里一律是 {@code TEXT}，靠这个转换器在边界上
 * 用 {@link BigDecimal#toPlainString()} / {@code new BigDecimal(String)} 往返 ——
 * 走字符串是唯一不丢精度的路径。
 *
 * <p>{@code autoApply = true}：所有实体的 BigDecimal 字段自动套用，不用逐字段写 @Convert。
 */
@Converter(autoApply = true)
public class BigDecimalTextConverter implements AttributeConverter<BigDecimal, String> {

    @Override
    public String convertToDatabaseColumn(BigDecimal value) {
        return value == null ? null : value.toPlainString();
    }

    @Override
    public BigDecimal convertToEntityAttribute(String stored) {
        return stored == null || stored.isBlank() ? null : new BigDecimal(stored);
    }
}
