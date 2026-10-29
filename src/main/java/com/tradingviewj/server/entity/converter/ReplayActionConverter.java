package com.tradingviewj.server.entity.converter;

import com.tradingviewj.server.enums.ReplayAction;
import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

/**
 * {@link ReplayAction} ↔ Go 协议里的动作字符串（{@code bar_advanced} …）。
 *
 * <p>存字符串而不是枚举名 {@code BAR_ADVANCED}：这一列的值会进
 * {@code trade_record_step.kind}，回放时前端拿它直接挑 reducer 分支。
 * 存成大写枚举名等于悄悄改了协议。
 */
@Converter(autoApply = true)
public class ReplayActionConverter implements AttributeConverter<ReplayAction, String> {

    @Override
    public String convertToDatabaseColumn(ReplayAction action) {
        return action == null ? null : action.wire();
    }

    @Override
    public ReplayAction convertToEntityAttribute(String wire) {
        return wire == null || wire.isBlank() ? null : ReplayAction.ofWire(wire);
    }
}
