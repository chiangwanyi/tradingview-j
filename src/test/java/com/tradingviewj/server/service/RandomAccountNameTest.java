package com.tradingviewj.server.service;

import org.junit.jupiter.api.Test;

import java.lang.reflect.Field;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** 随机名：词库够大、词条互不重复且不超过三字、拼出来的名字合规且有随机性。 */
class RandomAccountNameTest {

    /** N 由这五个库拼成（见 {@code RandomAccountName.NOUNS}）。 */
    private static final List<String> N_POOLS = List.of("DWELLINGS", "ANIMALS", "PLANTS", "FOODS", "TECH");

    /**
     * 科技词里不许出现的字根 —— 医疗、人体、生物技术（「骨传导」「核磁共振」这类词全部清掉了）。
     * 下面三个词虽含字根但与医学无关，单独放行。
     */
    private static final List<String> MEDICAL_ROOTS = List.of(
            "医", "病", "药", "症", "疗", "诊", "血", "骨", "菌", "毒", "解剖", "手术", "器官",
            "免疫", "细胞", "基因", "神经", "疫苗", "护理", "患者", "康复", "脉搏", "麻醉");

    private static final Set<String> MEDICAL_ROOT_EXCEPTIONS = Set.of("消毒柜", "龙骨车", "火药");

    /**
     * 动物词里不许出现的词，按四类：寄生虫 / 害虫 / 名字带「食人」或等同于「杀人鲸」的 / 传说瑞兽。
     * 这几个词已经从词库里删掉，测试只负责别让它们回来。
     */
    private static final List<String> BANNED_ANIMALS = List.of(
            // 寄生虫
            "蛔虫", "绦虫", "钩虫", "水蛭", "跳蚤",
            // 害虫
            "蚊子", "苍蝇", "家蝇", "蟑螂", "白蚁", "蝗虫", "蝼蛄", "蚜虫", "蚧壳虫", "粉虱",
            "蓟马", "潜叶蝇", "地老虎", "尺蠖", "螟蛾", "牛虻", "蠓虫", "田鼠", "衣鱼", "灶马", "蝽象",
            "果蝇", "天牛", "麝鼠", "海狸鼠",
            // 字面上危害人类
            "食人鱼", "虎鲸",
            // 虚构 / 传说
            "麒麟", "凤凰", "蛟龙", "应龙", "白泽", "饕餮", "穷奇", "梼杌", "九尾狐", "朱雀",
            "玄武", "貔貅", "獬豸", "鲲鹏", "龙马", "狻猊", "睚眦", "囚牛", "蒲牢", "霸下",
            "螭吻", "狴犴", "天马", "神鹿", "青鸟", "金乌", "玉兔", "鲲", "鹏", "鸾鸟",
            "重明鸟", "毕方", "英招", "陆吾", "帝江", "烛龙", "相柳", "天禄", "辟邪", "角端",
            "白鹿", "玄鸟", "青鸾");

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
                assertTrue(word.matches("[\\u4e00-\\u9fa5]{1,3}"),
                        name + " 里有非汉字或超过三字的词（四字及以上一律不收）：" + word);
            }
        }

        int nouns = 0;
        for (String name : N_POOLS) {
            nouns += pool(name).length;
        }
        // 限成三字以内后 N 掉到两千六上下（原来三千三），下限跟着调低
        assertTrue(nouns >= 2500, "N 只有 " + nouns + " 个词");
    }

    /** 科技词不和医疗、人体、生物技术沾边 —— 「骨传导」这类词不该拿来当账户名。 */
    @Test
    void techPoolHasNoMedicalOrBiotechWords() throws ReflectiveOperationException {
        for (String word : pool("TECH")) {
            if (MEDICAL_ROOT_EXCEPTIONS.contains(word)) {
                continue;
            }
            for (String root : MEDICAL_ROOTS) {
                assertFalse(word.contains(root), "科技词里有医疗 / 人体 / 生物技术相关的词：" + word);
            }
        }
    }

    /** 动物词只收看着顺眼的真兽：寄生虫、害虫、吃人的、传说中的，一律不收。 */
    @Test
    void animalsPoolHasNoPestsParasitesOrMythicalBeasts() throws ReflectiveOperationException {
        List<String> animals = List.of(pool("ANIMALS"));
        for (String banned : BANNED_ANIMALS) {
            assertFalse(animals.contains(banned), "动物词里混进了不该有的：「" + banned + "」");
        }
    }

    @Test
    void generatedNamesAreChineseNamesAndVary() {
        Set<String> seen = new HashSet<>();
        for (int i = 0; i < 2000; i++) {
            String name = RandomAccountName.next();
            // XY 形如「智慧的山谷」（五字），MN 形如「星河灯塔」；每词最多三字，故名字最长五字
            assertTrue(name.matches("[\\u4e00-\\u9fa5]{3,5}"), "名字不合规：" + name);
            seen.add(name);
        }
        // 组合空间五十多万种，2000 次里出现一千个以上不同结果才算真的随机
        assertTrue(seen.size() > 1000, "随机性太差，只有 " + seen.size() + " 个不同结果");
    }
}
