package com.tradingviewj.server.entity.audit.payload;

import com.tradingviewj.server.enums.ReplayAction;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Constructor;
import java.lang.reflect.RecordComponent;
import java.util.EnumMap;
import java.util.EnumSet;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 钉住「一个动作 = 一个负载类型」这条对应关系：{@link ReplayAction} 里每个取值都必须
 * 恰好被一个 record 认领，且每个认领者都是 record。
 *
 * <p>为什么要测：漏一个动作不会有任何编译错误 —— 只是将来做类型化读取时，那个动作
 * 悄悄落进 {@code default} 分支，一条流水就永远解析不出来。而这层唯一的价值就是这个映射，
 * 它错了等于没写。
 *
 * <p>怎么拿到 {@code action()}：反射出 record 的规范构造器，按各组件类型喂默认值
 * （{@code null} / 0 / false）造一个实例 —— 这些 record 刻意<strong>不做校验</strong>，
 * 所以能这么造。
 */
class ReplayActionPayloadTest {

    @Test
    void everyReplayActionHasExactlyOnePayloadType() throws ReflectiveOperationException {
        Class<?>[] permitted = ReplayActionPayload.class.getPermittedSubclasses();
        assertEquals(ReplayAction.values().length, permitted.length,
                "sealed 接口的允许实现数必须等于动作数");

        Map<ReplayAction, Class<?>> owner = new EnumMap<>(ReplayAction.class);
        for (Class<?> type : permitted) {
            assertTrue(type.isRecord(), type.getSimpleName() + " 必须是 record");
            assertEquals(ReplayActionPayload.class.getPackageName(), type.getPackageName(),
                    type.getSimpleName() + " 必须和接口同包（未命名模块里 sealed 只能包内继承）");
            ReplayAction action = actionOf(type);
            assertNull(owner.put(action, type),
                    "动作 " + action + " 被两个类型认领：" + owner.get(action) + " / " + type);
        }
        assertEquals(EnumSet.allOf(ReplayAction.class), owner.keySet(), "有动作没有任何负载类型");
    }

    @Test
    void onlyBaseAndRecordingAudioAreEmpty() {
        assertEquals(0, BasePayload.class.getRecordComponents().length);
        assertEquals(0, RecordingAudioPayload.class.getRecordComponents().length);

        for (Class<?> type : ReplayActionPayload.class.getPermittedSubclasses()) {
            if (type == BasePayload.class || type == RecordingAudioPayload.class) {
                continue;
            }
            assertTrue(type.getRecordComponents().length > 0,
                    type.getSimpleName() + " 不是空负载，却一个字段都没有");
        }
    }

    /** 造一个各组件都取默认值的实例，读它认领的动作。 */
    private static ReplayAction actionOf(Class<?> type) throws ReflectiveOperationException {
        RecordComponent[] components = type.getRecordComponents();
        Class<?>[] types = new Class<?>[components.length];
        Object[] args = new Object[components.length];
        for (int i = 0; i < components.length; i++) {
            types[i] = components[i].getType();
            args[i] = defaultValue(types[i]);
        }
        Constructor<?> canonical = type.getDeclaredConstructor(types);
        return ((ReplayActionPayload) canonical.newInstance(args)).action();
    }

    private static Object defaultValue(Class<?> type) {
        if (!type.isPrimitive()) {
            return null;
        }
        if (type == boolean.class) {
            return false;
        }
        if (type == int.class) {
            return 0;
        }
        if (type == long.class) {
            return 0L;
        }
        if (type == double.class) {
            return 0d;
        }
        if (type == float.class) {
            return 0f;
        }
        if (type == short.class) {
            return (short) 0;
        }
        if (type == byte.class) {
            return (byte) 0;
        }
        if (type == char.class) {
            return (char) 0;
        }
        return null;
    }
}
