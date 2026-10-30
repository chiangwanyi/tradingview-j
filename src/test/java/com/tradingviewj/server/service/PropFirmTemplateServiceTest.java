package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import javax.sql.DataSource;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.sql.Connection;
import java.sql.SQLException;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 模板的增删改查，用<strong>真实的 SQLite</strong>（临时目录里的一份库）钉住。
 *
 * <p>为什么不用 mock：这里要验的正好是 mock 测不到的那部分 —— 规则是 {@code @Embeddable}、
 * 金额是 {@code BigDecimal → TEXT} 转换、手数分档是 JSON 列。这些只有真连库才走得通。
 *
 * <p>业务库 {@code ./db/tradingview-j.db} <strong>一次都不碰</strong>：库文件开在临时目录，
 * {@link #testsNeverTouchTheBusinessDatabase()} 把这件事写成断言，防止有人后来把
 * {@code @DynamicPropertySource} 删掉之后「测试跑得很顺，只是悄悄改了业务数据」。
 */
@SpringBootTest
class PropFirmTemplateServiceTest {

    private static String testDbUrl;

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-template-").toAbsolutePath().toString();
            testDbUrl = "jdbc:sqlite:" + dir + "/test.db";
            registry.add("spring.datasource.url", () -> testDbUrl);
            // K 线目录也指到临时目录：本类读不到任何行情，但不能让任何 bean 摸到仓库里的 data/
            registry.add("app.bar-root", () -> dir + "/data");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private PropFirmTemplateService service;
    @Autowired private PropFirmTemplateRepository templates;
    @Autowired private DataSource dataSource;

    @BeforeEach
    void clean() {
        templates.deleteAll();
    }

    /** 临时库和业务库是两份文件。改这里之前先想清楚：不要让单测写进真实账户数据。 */
    @Test
    void testsNeverTouchTheBusinessDatabase() throws SQLException {
        try (Connection connection = dataSource.getConnection()) {
            assertEquals(testDbUrl, connection.getMetaData().getURL());
            assertTrue(!testDbUrl.replace('\\', '/').contains("/db/tradingview-j.db"),
                    "测试库不能是业务库: " + testDbUrl);
        }
    }

    /** 建的时候写进去的每一个字段，都要能从库里原样读回来（含 JSON 列和金额精度）。 */
    @Test
    void createPersistsEveryFieldIncludingRules() {
        PropFirmTemplate created = service.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                null, evalRules());

        assertNotNull(created.getId());
        assertNotNull(created.getCreatedAt());
        assertNotNull(created.getUpdatedAt());

        PropFirmTemplate loaded = service.get(created.getId());
        assertEquals("LucidFlex 50K EVAL", loaded.getName());
        assertEquals("考试", loaded.getDescription());
        assertEquals(PropFirmStage.EVAL, loaded.getStage());
        assertNull(loaded.getNextTemplateId());

        PropFirmRules rules = loaded.getRules();
        assertEquals(new BigDecimal("50000.00"), rules.getAccountSize(), "金额必须按字符串往返，一位都不能丢");
        assertEquals(new BigDecimal("3000.00"), rules.getProfitTarget());
        assertEquals(new BigDecimal("2000.00"), rules.getMaxDrawdown());
        assertEquals(PropFirmRules.DrawdownType.TRAILING_EOD, rules.getDrawdownType());
        assertEquals(new BigDecimal("52100.00"), rules.getTrailingLockLevel());
        assertEquals(new BigDecimal("0.50"), rules.getConsistencyRatio());
        assertNull(rules.getMinProfitDays(), "EVAL 不查盈利日，null 是有意义的取值，不是漏配");
        assertEquals(List.of(new PositionTier(BigDecimal.ZERO, 40)), rules.getPositionTiers(),
                "手数分档走 JSON 列，往返后必须一模一样");
    }

    /** 阶段与规则矛盾的模板建不出来，失败后库里一条残留都没有。 */
    @Test
    void createRejectsStageRuleMismatch() {
        // FUNDED 还带盈利目标 —— 那其实是第二场考试，会让账户永远升不了级
        assertThrows(IllegalArgumentException.class, () -> service.create("错标的出金号", null,
                PropFirmStage.FUNDED, null, evalRules()));

        // EVAL 没有盈利目标 —— 没有目标就不是考试
        PropFirmRules noTarget = new PropFirmRules(new BigDecimal("50000.00"), null,
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD, null, null,
                null, null, List.of());
        assertThrows(IllegalArgumentException.class, () -> service.create("没有目标的考试", null,
                PropFirmStage.EVAL, null, noTarget));

        // 缺名字
        assertThrows(IllegalArgumentException.class, () -> service.create("  ", null,
                PropFirmStage.EVAL, null, evalRules()));

        assertEquals(0, templates.count(), "校验失败不该留下半条模板");
    }

    /** 改：字段换成新的，身份（id / 创建时间）不变，阶段不变。 */
    @Test
    void updateRewritesFieldsAndKeepsIdentity() {
        PropFirmTemplate created = service.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                null, evalRules());
        String id = created.getId();
        // 取落库后的时间：内存里的 Instant 带纳秒，库里的列只到毫秒，直接比会差出精度
        var createdAt = service.get(id).getCreatedAt();

        PropFirmTemplate updated = service.update(id, "LucidFlex 50K 考试", "改过的说明", "next-1",
                evalRules().copy());

        assertEquals(id, updated.getId());
        assertEquals(createdAt, service.get(id).getCreatedAt(), "创建时间一旦写入不再改动");
        assertEquals(PropFirmStage.EVAL, updated.getStage(), "阶段不是可改字段");

        PropFirmTemplate loaded = service.get(id);
        assertEquals("LucidFlex 50K 考试", loaded.getName());
        assertEquals("改过的说明", loaded.getDescription());
        assertEquals("next-1", loaded.getNextTemplateId(), "next_template_id 由后续这次更新接上");
        assertEquals(1, templates.count(), "改的是同一行，不是新插一行");
    }

    /** 改坏了（规则与阶段矛盾）必须当场失败，而且库里那一行原样不动。 */
    @Test
    void updateRejectsStageRuleMismatchAndLeavesRowUntouched() {
        PropFirmTemplate created = service.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                null, evalRules());

        PropFirmRules noTarget = new PropFirmRules(new BigDecimal("50000.00"), null,
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD, null, null,
                null, null, List.of());
        assertThrows(IllegalArgumentException.class,
                () -> service.update(created.getId(), "抹掉目标", null, null, noTarget));

        PropFirmTemplate loaded = service.get(created.getId());
        assertEquals("LucidFlex 50K EVAL", loaded.getName());
        assertEquals(new BigDecimal("3000.00"), loaded.getRules().getProfitTarget());
    }

    /** 改一条不存在的模板 → 业务异常（不是 500，也不是静默插入一条新的）。 */
    @Test
    void updateMissingTemplateFailsAsBusinessError() {
        BusinessException e = assertThrows(BusinessException.class,
                () -> service.update("没有这个模板", "x", null, null, evalRules()));
        assertTrue(e.getMessage().contains("模板不存在"));
        assertEquals(0, templates.count());
    }

    @Test
    void getMissingTemplateFailsAsBusinessError() {
        BusinessException e = assertThrows(BusinessException.class, () -> service.get("没有这个模板"));
        assertTrue(e.getMessage().contains("模板不存在"));
    }

    /** 列表给全部模板；链上的两段都在。 */
    @Test
    void listReturnsEveryTemplate() {
        PropFirmTemplate funded = service.create("LucidFlex 50K FUNDED", "出金号",
                PropFirmStage.FUNDED, null, fundedRules());
        PropFirmTemplate eval = service.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                funded.getId(), evalRules());

        List<PropFirmTemplate> all = service.list();
        assertEquals(2, all.size());
        assertTrue(all.stream().anyMatch(t -> t.getId().equals(eval.getId())));
        assertTrue(all.stream().anyMatch(t -> t.getId().equals(funded.getId())));
    }

    /** 删是幂等的，删掉的模板真没了。 */
    @Test
    void deleteRemovesTheRowAndIsIdempotent() {
        PropFirmTemplate created = service.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                null, evalRules());

        service.delete(created.getId());
        assertTrue(templates.findById(created.getId()).isEmpty());

        // 删除一个已经不存在的模板，结果仍然是「它没了」，不该抛异常
        service.delete(created.getId());
        assertEquals(0, templates.count());
    }

    private static PropFirmRules evalRules() {
        return new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                List.of(new PositionTier(BigDecimal.ZERO, 40)));
    }

    private static PropFirmRules fundedRules() {
        return new PropFirmRules(new BigDecimal("50000.00"), null, new BigDecimal("2000.00"),
                PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100.00"), null,
                5, new BigDecimal("150.00"),
                List.of(new PositionTier(BigDecimal.ZERO, 20),
                        new PositionTier(new BigDecimal("1000"), 30),
                        new PositionTier(new BigDecimal("2000"), 40)));
    }
}
