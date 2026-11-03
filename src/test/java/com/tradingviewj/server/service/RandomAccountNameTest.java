package com.tradingviewj.server.service;

import org.junit.jupiter.api.Test;

import java.lang.reflect.Field;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertTrue;

/** 随机名：词库够大、全部词条互不重复，拼出来的名字合规且有随机性。 */
class RandomAccountNameTest {

    /** N 由这五个库拼成（见 {@code RandomAccountName.NOUNS}）。 */
    private static final List<String> N_POOLS = List.of("DWELLINGS", "ANIMALS", "PLANTS", "FOODS", "TECH");

    private static String[] pool(String name) throws ReflectiveOperationException {
        Field field = RandomAccountName.class.getDeclaredField(name);
        field.setAccessible(true);
        return (String[]) field.get(null);
    }

    @Test
    void everyPoolIsLargeAndAllWordsAreUnique() throws ReflectiveOperationException {
        Set<String> all = new HashSet<>();
        for (String name : List.of("ADJECTIVES", "LANDSCAPES", "SKIES", "DWELLINGS", "ANIMALS", "PLANTS", "FOODS", "TECH")) {
            String[] words = pool(name);
            assertTrue(words.length >= 200, name + " 只有 " + words.length + " 个词");
            for (String word : words) {
                assertTrue(all.add(word), "重复词目（跨库也不允许）：" + word);
                assertTrue(word.matches("[\\u4e00-\\u9fa5]{1,6}"), name + " 里有非汉字词：" + word);
            }
        }

        int nouns = 0;
        for (String name : N_POOLS) {
            nouns += pool(name).length;
        }
        assertTrue(nouns >= 3000, "N 只有 " + nouns + " 个词");
    }

    @Test
    void generatedNamesAreChineseNamesAndVary() {
        Set<String> seen = new HashSet<>();
        for (int i = 0; i < 2000; i++) {
            String name = RandomAccountName.next();
            // XY 形如「智慧的山谷」，MN 形如「星河灯塔」；最长的是「自然语言处理」这类六字名词
            assertTrue(name.matches("[\\u4e00-\\u9fa5]{3,9}"), "名字不合规：" + name);
            seen.add(name);
        }
        // 组合空间五万多种，2000 次里出现一千个以上不同结果才算真的随机
        assertTrue(seen.size() > 1000, "随机性太差，只有 " + seen.size() + " 个不同结果");
    }
}
