package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AnnotationStyleView;
import com.tradingviewj.server.dto.TemplateView;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AnnotationTemplateRepository;
import com.tradingviewj.server.ws.TemplateCommand;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 模板的落库与约束，用<strong>真实的 SQLite</strong>（临时库）钉住。
 *
 * <p>和 {@code PropFirmTemplateServiceTest} 同一个理由：这里要验的正好是 mock 测不到的部分 ——
 * 样式是一整块 JSON 列，往返之后宽度精度、斐波那契四条水平线必须一模一样。
 *
 * <p>业务库 {@code ./db/tradingview-j.db} 一次都不碰。
 */
@SpringBootTest
class AnnotationTemplateServiceTest {

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-annotation-template-").toAbsolutePath().toString();
            registry.add("spring.datasource.url", () -> "jdbc:sqlite:" + dir + "/test.db");
            registry.add("app.bar-root", () -> dir + "/data");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @Autowired private AnnotationTemplateService service;
    @Autowired private AnnotationTemplateRepository templates;

    @BeforeEach
    void clean() {
        templates.deleteAll();
    }

    /** 表里没有模板时列表是空的 —— 出厂样式由前端兜底，不在库里预置。 */
    @Test
    void listIsEmptyWhenNothingSaved() {
        assertTrue(service.list().isEmpty(), "不预置出厂模板：删光了就不该再冒出来");
    }

    /** 存一条再读回来，样式里的每个字段都要原样（含斐波那契四条线）。 */
    @Test
    void savePersistsStyleIncludingFibLevels() {
        TemplateView saved = service.save(new TemplateCommand(null, "fib", "四色实线",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.2"), "solid", null, null,
                        List.of(new AnnotationStyleView.FibLevelView(-1, "#38bdf8"),
                                new AnnotationStyleView.FibLevelView(0, "#f59e0b"),
                                new AnnotationStyleView.FibLevelView(1, "#ec4899"),
                                new AnnotationStyleView.FibLevelView(2, "#22c55e")))));

        assertNotNull(saved.id());
        assertEquals("fib", saved.kind(), "协议里的 kind 是小写");

        TemplateView loaded = service.list().get(0);
        assertEquals(saved.id(), loaded.id());
        assertEquals("四色实线", loaded.name());
        assertEquals("solid", loaded.style().dash());
        assertEquals(0, loaded.style().width().compareTo(new BigDecimal("1.2")));
        assertEquals(4, loaded.style().levels().size(), "四条水平线走 JSON 列，往返后一条都不能少");
        assertEquals(-1, loaded.style().levels().get(0).ratio());
        assertEquals("#ec4899", loaded.style().levels().get(2).color());
    }

    /** 越界的样式在写库之前就被夹回来：手改客户端能把 width 写成 400，库里不能留这种值。 */
    @Test
    void saveClampsOutOfRangeStyle() {
        service.save(new TemplateCommand(null, "trend", "   ",
                new AnnotationStyleView("nonsense", new BigDecimal("400"), "wavy", null, null, null)));

        TemplateView loaded = service.list().get(0);
        assertEquals(6, loaded.style().width().intValue(), "线宽夹到上限");
        assertEquals("#f59e0b", loaded.style().color(), "非法颜色退回出厂色");
        assertEquals("solid", loaded.style().dash(), "只有 solid/dashed 两种");
        assertEquals("趋势线", loaded.name(), "空名字用工具名顶上，不能留一个看不见的 chip");
    }

    /** 名字超过 24 字被截断（界面上输入框的 maxlength 就是这个数）。 */
    @Test
    void saveTruncatesLongName() {
        service.save(new TemplateCommand(null, "trend", "一二三四五六七八九十一二三四五六七八九十一二三四五",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));
        assertEquals(AnnotationStyleRules.MAX_NAME_LENGTH, service.list().get(0).name().length());
    }

    /** 矩形没有填充色时，填充跟随边框 —— 和前端 {@code defaultStyle} 同一套规则。 */
    @Test
    void rectFillFallsBackToBorderColor() {
        service.save(new TemplateCommand(null, "rect", "蓝框",
                new AnnotationStyleView("#0ea5e9", new BigDecimal("1.4"), "solid", null, null, null)));
        TemplateView loaded = service.list().get(0);
        assertEquals("#0ea5e9", loaded.style().fill());
        assertEquals(0, loaded.style().fillOpacity().compareTo(new BigDecimal("0.12")));
    }

    /** 覆盖时不动 {@code createdAt}：改个颜色不该让 chip 跳到列表末尾。 */
    @Test
    void updateKeepsCreatedAt() {
        TemplateView first = service.save(new TemplateCommand(null, "trend", "旧名",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));
        service.save(new TemplateCommand(null, "ray", "中间的",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));

        TemplateView updated = service.save(new TemplateCommand(first.id(), "trend", "新名",
                new AnnotationStyleView("#22c55e", new BigDecimal("2"), "dashed", null, null, null)));

        assertEquals(first.id(), updated.id());
        assertEquals("新名", updated.name());
        assertEquals(first.createdAt(), updated.createdAt(), "创建时间决定 chip 顺序，覆盖不能改它");
        assertEquals("#22c55e", service.list().get(0).style().color(), "第一条还是它");
    }

    /** 不能把一条模板改成另一个工具下的 —— 那等于在原来的工具下面凭空抽走一条。 */
    @Test
    void saveRejectsKindChange() {
        TemplateView saved = service.save(new TemplateCommand(null, "trend", "金色",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));
        BusinessException e = assertThrows(BusinessException.class, () -> service.save(
                new TemplateCommand(saved.id(), "rect", "金色",
                        new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null))));
        assertTrue(e.getMessage().contains("新建一条"), e.getMessage());
    }

    /** 单个工具最多 20 条：列表是全量广播的，攒到几百条会拖慢每一次操作。 */
    @Test
    void saveRejectsWhenKindIsFull() {
        for (int i = 0; i < AnnotationStyleRules.MAX_TEMPLATES_PER_KIND; i++) {
            service.save(new TemplateCommand(null, "trend", "模板 " + i,
                    new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));
        }
        BusinessException e = assertThrows(BusinessException.class, () -> service.save(
                new TemplateCommand(null, "trend", "第 21 条",
                        new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null))));
        assertTrue(e.getMessage().contains("20"), e.getMessage());

        // 别的工具不受影响：上限是「每个工具」而不是「一共」
        TemplateView other = service.save(new TemplateCommand(null, "rect", "蓝框",
                new AnnotationStyleView("#0ea5e9", new BigDecimal("1.4"), "solid", null, null, null)));
        assertEquals("rect", other.kind());
    }

    /** 删一条不存在的模板要报错，不能静默成功（两个标签页时一定会遇到）。 */
    @Test
    void deleteRejectsMissingTemplate() {
        BusinessException e = assertThrows(BusinessException.class, () -> service.delete("tpl-999"));
        assertTrue(e.getMessage().contains("可能已被删掉"), e.getMessage());
        assertThrows(BusinessException.class, () -> service.delete("  "));
    }

    /** 删掉之后列表里就没有了，而且别的工具下的不受影响。 */
    @Test
    void deleteRemovesOnlyThatTemplate() {
        TemplateView trend = service.save(new TemplateCommand(null, "trend", "金色",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null)));
        TemplateView rect = service.save(new TemplateCommand(null, "rect", "蓝框",
                new AnnotationStyleView("#0ea5e9", new BigDecimal("1.4"), "solid", null, null, null)));

        service.delete(trend.id());

        List<TemplateView> left = service.list();
        assertEquals(1, left.size());
        assertEquals(rect.id(), left.get(0).id());
        assertNotEquals(trend.id(), left.get(0).id());
    }

    /** 不认识的工具名要被拒（模板属于某个确定的工具，没有「通用模板」）。 */
    @Test
    void saveRejectsUnknownKind() {
        assertThrows(BusinessException.class, () -> service.save(new TemplateCommand(
                null, "channel", "通道",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null))));
        assertThrows(BusinessException.class, () -> service.save(new TemplateCommand(
                null, null, "没类型",
                new AnnotationStyleView("#f59e0b", new BigDecimal("1.6"), "solid", null, null, null))));
    }

    /** {@code kind} 的协议串和枚举是同一个集合，两个方向都要通。 */
    @Test
    void kindWireRoundTrips() {
        for (Drawing.Kind kind : Drawing.Kind.values()) {
            String wire = AnnotationStyleRules.wire(kind);
            assertEquals(kind, AnnotationStyleRules.kindOf(wire), wire + " 必须能转回去");
            assertEquals(wire, wire.toLowerCase(), "协议串一律小写");
        }
        assertNull(AnnotationStyleRules.wire(null));
    }
}
