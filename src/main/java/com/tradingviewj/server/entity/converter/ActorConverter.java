package com.tradingviewj.server.entity.converter;

import com.tradingviewj.server.enums.Actor;
import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

/**
 * {@link Actor} ↔ 小写线值（{@code human} / {@code bot} / {@code system}）。
 *
 * <p>{@code autoApply = true}：所有 {@code Actor} 字段自动套用，不用逐字段写 {@code @Convert}，
 * 也<strong>不要</strong>再加 {@code @Enumerated} —— 两者同时出现会冲突。
 */
@Converter(autoApply = true)
public class ActorConverter implements AttributeConverter<Actor, String> {

    @Override
    public String convertToDatabaseColumn(Actor actor) {
        return actor == null ? null : actor.wire();
    }

    @Override
    public Actor convertToEntityAttribute(String wire) {
        return wire == null || wire.isBlank() ? null : Actor.ofWire(wire);
    }
}
