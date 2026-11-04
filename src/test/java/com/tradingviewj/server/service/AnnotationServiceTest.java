package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AnnotationPointView;
import com.tradingviewj.server.dto.AnnotationStyleView;
import com.tradingviewj.server.dto.AnnotationView;
import com.tradingviewj.server.entity.audit.payload.AnnotationClearedPayload;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.DrawingRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.ws.AnnotationCommand;
import com.tradingviewj.server.ws.TradeDeskService;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Comparator;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.when;

/**
 * 图形的落库、归属边界与锚点校验，用<strong>真实的 SQLite</strong>（临时库）钉住。
 *
 * <p>三条必须用真库才验得到的：
 * <ul>
 *   <li>{@code start/end} 是 {@code @Embedded} 的 {@link Anchor}，列名靠
 *       {@code @AttributeOverride} 指定 —— mock 测不到列名写错（那种错只在启动时红）；</li>
 *   <li>斐波那契四条线走 {@code @ElementCollection} + {@code drawing_fib_level} 的级联；</li>
 *   <li>图形的归属是 {@code symbol_id} 而不是账户 —— 换个账户做同一张图，线必须还在。</li>
 * </ul>
 *
 * <p><strong>锚点的时间基准是 {@code REVEALED}</strong>：账户刚开出来就停在 09:00，
 * 09:05 / 09:10 那两根在磁盘上存在但还没走。测试里所有「合法的线」都锚在 09:00，
 * 右端靠 {@code offset} 往右推 —— 这正是前端表达「画到最后一根右边」的方式。
 * 需要锚在更晚的根时，先 {@code step} 一根把盘面推过去（见
 * {@link #anchorMaySitOnAnEarlierRevealedBar}）。
 */
@SpringBootTest
class AnnotationServiceTest {

    private static final String SYMBOL = "CME_MINI:MES1!";
    private static final String FOLDER = "CME_MINI_MES1!";
    /** 第二个标的：用来验「跨标的改不动别人的线」。 */
    private static final String OTHER = "CME_MINI:MNQ1!";
    private static final String OTHER_FOLDER = "CME_MINI_MNQ1!";
    /** 账户开出来就停在这一根 —— 合法的锚点上界。 */
    private static final String REVEALED = "2024-03-04 09:00";

    private static String testDbUrl;
    private static Path dataDir;

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            Path dir = Files.createTempDirectory("tvj-annotation-").toAbsolutePath();
            testDbUrl = "jdbc:sqlite:" + dir.resolve("test.db");
            dataDir = dir.resolve("data");
            registry.add("spring.datasource.url", () -> testDbUrl);
            registry.add("app.bar-root", () -> dataDir.toString());
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @MockitoBean
    private PropFirmStartPicker picker;

    @Autowired private AnnotationService annotations;
    @Autowired private TradeDeskService desk;
    @Autowired private DrawingRepository drawings;
    @Autowired private AuditEventRepository auditRepo;
    @Autowired private PropFirmAccountService accounts;
    @Autowired private PropFirmTemplateService templates;
    @Autowired private PropFirmAccountRepository accountRepo;
    @Autowired private PropFirmTemplateRepository templateRepo;
    @Autowired private SymbolRepository symbolRepo;

    private String accountId;

    @BeforeEach
    void clean() {
        auditRepo.deleteAll();
        drawings.deleteAll();
        accountRepo.deleteAll();
        templateRepo.deleteAll();
        symbolRepo.deleteAll();
        clearBars();
        writeSymbols();

        com.tradingviewj.server.entity.market.Symbol mes =
                com.tradingviewj.server.entity.market.Symbol.of(SYMBOL, FOLDER, new BigDecimal("0.50"));
        mes.setTick(new BigDecimal("0.25"));
        mes.setTickValue(new BigDecimal("0.5"));
        symbolRepo.save(mes);
        com.tradingviewj.server.entity.market.Symbol mnq =
                com.tradingviewj.server.entity.market.Symbol.of(OTHER, OTHER_FOLDER, new BigDecimal("0.50"));
        mnq.setTick(new BigDecimal("0.25"));
        mnq.setTickValue(new BigDecimal("0.5"));
        symbolRepo.save(mnq);

        writeDay(FOLDER, "2024-03-04",
                "2024-03-04 09:00,4000.00,4002.00,3998.00,4001.00,10",
                "2024-03-04 09:05,4001.00,4005.00,3999.00,4004.00,12",
                "2024-03-04 09:10,4004.00,4006.00,4000.00,4002.00,9");
        writeDay(OTHER_FOLDER, "2024-03-04",
                "2024-03-04 09:00,15000.00,15002.00,14998.00,15001.00,10");

        when(picker.pick(anyString())).thenReturn(
                new PropFirmStartPicker.Pick(FOLDER, REVEALED, "2024-03-04.csv"));

        PropFirmTemplate eval = templates.create("EVAL 50K", "考试", PropFirmStage.EVAL,
                null, new PropFirmRules(new BigDecimal("50000.00"), new BigDecimal("3000.00"),
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("52100.00"), new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, 40))));
        PropFirmAccount account = accounts.open(eval.getId(), SYMBOL, "画线测试账户");
        accountId = account.getId();
        assertEquals(REVEALED, account.getLastBarTime(), "账户建出来就停在起点，测试的前提");
    }

    // ---------------- 画 ----------------

    /** 画一条线：落库、字段原样、写一条 {@code annotation_added} 流水。 */
    @Test
    void addPersistsEveryFieldAndWritesAudit() {
        TradingService.Event event = annotations.add(accountId, trend("a1", "3990.00", "4010.00"));

        assertEquals("annotation_added", event.action().wire());
        assertEquals(1, auditRepo.count(), "画线不改余额，但它是盘面上的一个动作，要进流水");
        assertEquals(1, drawings.count());

        AnnotationView view = annotations.list(SYMBOL).get(0);
        assertEquals("a1", view.id());
        assertEquals("trend", view.kind(), "协议里的 kind 是小写，库里存的是 TREND");
        assertEquals(REVEALED, view.start().time());
        assertEquals("3990.00", view.start().price().toPlainString());
        assertEquals(0, view.start().offset());
        assertEquals(3, view.end().offset(), "右端靠 offset 推出去，不靠一个未来的时间");
        assertEquals("#ff0000", view.style().color());
        assertEquals("dashed", view.style().dash());
        assertEquals(2, view.style().width().intValue());
        assertTrue(view.snapped());
        assertNull(view.style().levels(), "趋势线没有斐波那契水平线");
    }

    /**
     * 锚点不许落在<strong>还没揭示</strong>的那一根。
     *
     * <p>账户停在 09:00，09:10 那根在磁盘上存在但还没走 —— 允许锚定它等于把后面的行情
     * 提前透露给使用者，而且前端 {@code xOf()} 找不到这根会整条不画。
     */
    @Test
    void addRejectsAnchorBeyondRevealedBar() {
        BusinessException e = assertThrows(BusinessException.class, () -> annotations.add(accountId,
                new AnnotationCommand("a1", "trend",
                        new AnnotationPointView(REVEALED, new BigDecimal("3990"), 0),
                        new AnnotationPointView("2024-03-04 09:10", new BigDecimal("4010"), 0),
                        style(), true)));
        assertTrue(e.getMessage().contains("还没走到"), "要说清楚是哪条规则挡住了：" + e.getMessage());
        assertEquals(0, drawings.count(), "拒绝之后不能留半条数据");
        assertEquals(0, auditRepo.count());
    }

    /** 推进一根之后，"刚才那根"就成了合法的锚点 —— 边界是「已揭示」，不是「只能是最后一根」。 */
    @Test
    void anchorMaySitOnAnEarlierRevealedBar() {
        desk.step(accountId); // 盘面推到 09:05

        annotations.add(accountId, new AnnotationCommand("a1", "trend",
                new AnnotationPointView(REVEALED, new BigDecimal("3990"), 0),
                new AnnotationPointView("2024-03-04 09:05", new BigDecimal("4010"), 0),
                style(), true));

        AnnotationView view = annotations.list(SYMBOL).get(0);
        assertEquals(REVEALED, view.start().time());
        assertEquals("2024-03-04 09:05", view.end().time(), "已经走过的那根可以当锚点");
    }

    /** 前端给的 id 撞号要被拒：重号的后果是「拖一条线，另一条跟着动」。 */
    @Test
    void addRejectsDuplicateId() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));
        assertThrows(BusinessException.class,
                () -> annotations.add(accountId, trend("a1", "3950.00", "4050.00")));
        assertEquals(1, drawings.count());
    }

    /** 不认识的工具名要被拒，不能悄悄当成趋势线存下去。 */
    @Test
    void addRejectsUnknownKind() {
        assertThrows(BusinessException.class, () -> annotations.add(accountId,
                new AnnotationCommand("a1", "channel",
                        new AnnotationPointView(REVEALED, new BigDecimal("3990"), 0),
                        new AnnotationPointView(REVEALED, new BigDecimal("4010"), 3),
                        style(), true)));
    }

    /** 没有起点的空中图形画不出来（锚点是唯一让它在屏幕上有位置的依据）。 */
    @Test
    void addRejectsMissingAnchor() {
        assertThrows(BusinessException.class, () -> annotations.add(accountId,
                new AnnotationCommand("a1", "trend", null,
                        new AnnotationPointView(REVEALED, new BigDecimal("4010"), 0),
                        style(), true)));
        assertEquals(0, drawings.count());
    }

    /** 斐波那契固定四条水平线，比例只认 -1/0/1/2，顺序也固定。 */
    @Test
    void fibAlwaysKeepsFourLevels() {
        AnnotationStyleView style = new AnnotationStyleView("#f59e0b", new BigDecimal("1.2"), "solid",
                null, null,
                // 故意多给一条比例 3、少给一条 2 —— 越界的丢掉，缺的补出厂色
                List.of(new AnnotationStyleView.FibLevelView(0, "#123456"),
                        new AnnotationStyleView.FibLevelView(1, "#654321"),
                        new AnnotationStyleView.FibLevelView(3, "#ffffff")));
        annotations.add(accountId, new AnnotationCommand("f1", "fib",
                new AnnotationPointView(REVEALED, new BigDecimal("4000"), 0),
                new AnnotationPointView(REVEALED, new BigDecimal("4010"), 4),
                style, false));

        List<AnnotationStyleView.FibLevelView> levels = annotations.list(SYMBOL).get(0).style().levels();
        assertEquals(List.of(-1, 0, 1, 2),
                levels.stream().map(AnnotationStyleView.FibLevelView::ratio).toList());
        assertEquals("#123456", levels.get(1).color(), "用户给的 0 号色要保住");
        assertEquals("#38bdf8", levels.get(0).color(), "没给的 -1 补出厂色");
        assertEquals("#22c55e", levels.get(3).color(), "漏掉的 2 补出厂色");
    }

    // ---------------- 改 ----------------

    /** 挪端点只改位置，样式不动。 */
    @Test
    void moveChangesAnchorsAndKeepsStyle() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));

        TradingService.Event event = annotations.move(accountId, new AnnotationCommand(
                "a1", "trend",
                new AnnotationPointView(REVEALED, new BigDecimal("4000"), 2),
                new AnnotationPointView(REVEALED, new BigDecimal("4020"), 5),
                null, false));

        assertEquals("annotation_moved", event.action().wire());
        AnnotationView view = annotations.list(SYMBOL).get(0);
        assertEquals(2, view.start().offset());
        assertEquals(5, view.end().offset());
        // 用 compareTo 而不是 equals/toPlainString：4020 和 4020.00 在账上是同一个数，
        // 这种「精度不同」的失败和「算错了」长得一模一样
        assertEquals(0, view.end().price().compareTo(new BigDecimal("4020")));
        assertEquals("#ff0000", view.style().color(), "挪端点不该动样式");
        assertEquals(2, auditRepo.count(), "加一条 + 挪一条");
    }

    /** 换样式只改外观，端点不动 —— 这是它和 {@code move} 分成两条动作的全部意义。 */
    @Test
    void styleChangesStyleAndKeepsAnchors() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));

        TradingService.Event event = annotations.style(accountId, new AnnotationCommand(
                "a1", "trend", null, null,
                new AnnotationStyleView("#00ff00", new BigDecimal("4"), "solid", null, null, null),
                null));

        assertEquals("annotation_styled", event.action().wire());
        AnnotationView view = annotations.list(SYMBOL).get(0);
        assertEquals("#00ff00", view.style().color());
        assertEquals(4, view.style().width().intValue());
        assertEquals("solid", view.style().dash());
        assertEquals("3990.00", view.start().price().toPlainString(), "换样式不该动端点");
    }

    /** 样式越界由后端夹回来：手改客户端能把 width 写成 400，库里不能留这种值。 */
    @Test
    void styleIsClampedOnTheWayIn() {
        annotations.add(accountId, new AnnotationCommand("a1", "trend",
                new AnnotationPointView(REVEALED, new BigDecimal("3990"), 0),
                new AnnotationPointView(REVEALED, new BigDecimal("4010"), 3),
                new AnnotationStyleView("red;}", new BigDecimal("400"), "wavy", null, null, null),
                false));

        AnnotationView view = annotations.list(SYMBOL).get(0);
        assertEquals(6, view.style().width().intValue(), "线宽夹到上限");
        assertEquals("#f59e0b", view.style().color(), "非法颜色（能拼进 SVG 属性）退回出厂色");
        assertEquals("solid", view.style().dash(), "只有 solid/dashed 两种");
    }

    // ---------------- 删 ----------------

    /** 删一条只删这一条。 */
    @Test
    void deleteRemovesOnlyThatOne() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));
        annotations.add(accountId, trend("a2", "3950.00", "4050.00"));

        TradingService.Event event = annotations.delete(accountId, "a1");

        assertEquals("annotation_deleted", event.action().wire());
        assertEquals(1, drawings.count());
        assertEquals("a2", annotations.list(SYMBOL).get(0).id());
    }

    /**
     * 删不到<strong>另一块盘面</strong>上的图形。
     *
     * <p>图形不挂账户，靠的是 {@code symbol_id}。不校验它的话，拿到另一个标的的图形 id
     * 就能把它删掉 —— 而用户在 A 的图上，看不到 B 上少了什么。
     */
    @Test
    void deleteCannotReachAnotherSymbol() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));
        Drawing other = otherSymbolDrawing();
        drawings.save(other);

        BusinessException e = assertThrows(BusinessException.class,
                () -> annotations.delete(accountId, other.getId()));
        assertTrue(e.getMessage().contains("没有图形"), e.getMessage());
        assertEquals(2, drawings.count(), "别的标的上的线一根都不能少");
    }

    /** 清空只清这块盘面，并且把被清掉的编号都报出来（事后回看要知道清的是哪几个）。 */
    @Test
    void clearRemovesAllOnThisSymbolOnly() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));
        annotations.add(accountId, trend("a2", "3950.00", "4050.00"));
        Drawing other = otherSymbolDrawing();
        drawings.save(other);

        TradingService.Event event = annotations.clear(accountId);

        assertEquals("annotation_cleared", event.action().wire());
        AnnotationClearedPayload payload = (AnnotationClearedPayload) event.payload();
        assertEquals(2, payload.count());
        assertEquals(List.of("a1", "a2"), payload.annotationIds());
        assertEquals(1, drawings.count(), "另一个标的上的线留着");
        assertEquals(OTHER, drawings.findAll().get(0).getSymbolId());
    }

    /** 空盘面上点「清空」不算错：照样记一条流水（用户确实点了那个按钮），只是 count 为 0。 */
    @Test
    void clearOnEmptyDeskStillRecords() {
        AnnotationClearedPayload payload =
                (AnnotationClearedPayload) annotations.clear(accountId).payload();
        assertEquals(0, payload.count());
        assertTrue(payload.annotationIds().isEmpty());
        assertEquals(1, auditRepo.count());
    }

    // ---------------- 归属 ----------------

    /**
     * 图形<strong>不挂账户</strong>：换个账户做同一张图，昨天画的线还在。
     *
     * <p>这是 {@code schema.sql} 第 4 条那条边界的行为面 —— 给图形挂上账户，
     * 「同一条线换个账户就消失」就会变成一种需要解释的行为。
     */
    @Test
    void drawingsSurviveAnotherAccountAndAreNotOwnedByIt() {
        annotations.add(accountId, trend("a1", "3990.00", "4010.00"));

        PropFirmTemplate eval = templateRepo.findAll().get(0);
        PropFirmAccount second = accounts.open(eval.getId(), SYMBOL, "另一个账户");

        assertEquals(1, annotations.list(SYMBOL).size(), "同一个标的的另一账户照样看得到");
        assertNotNull(second.getId());
        assertNotEquals(accountId, second.getId());
    }

    /**
     * 列表按创建顺序给：前端画布的图层顺序就是它，重启后不该自己重排。
     *
     * <p>不按 id 字符串排（{@code a10} 会跑到 {@code a2} 前面），也不按时间倒序。
     */
    @Test
    void listKeepsCreationOrder() {
        annotations.add(accountId, trend("z-last", "3990.00", "4010.00"));
        annotations.add(accountId, trend("a-first", "3950.00", "4050.00"));

        assertEquals(List.of("z-last", "a-first"),
                annotations.list(SYMBOL).stream().map(AnnotationView::id).toList());
    }

    // ---------------- 辅助 ----------------

    private static AnnotationStyleView style() {
        return new AnnotationStyleView("#ff0000", new BigDecimal("2"), "dashed", null, null, null);
    }

    /** 一条合法的趋势线：两端都锚在已揭示的那根上，右端靠 offset 推出去。 */
    private static AnnotationCommand trend(String id, String startPrice, String endPrice) {
        return new AnnotationCommand(id, "trend",
                new AnnotationPointView(REVEALED, new BigDecimal(startPrice), 0),
                new AnnotationPointView(REVEALED, new BigDecimal(endPrice), 3),
                style(), true);
    }

    private static Drawing otherSymbolDrawing() {
        return Drawing.of(OTHER, Drawing.Kind.TREND,
                new Anchor(REVEALED, new BigDecimal("15000"), 0),
                new Anchor(REVEALED, new BigDecimal("15010"), 0),
                "human");
    }

    private void clearBars() {
        if (!Files.exists(dataDir)) {
            return;
        }
        try (var walk = Files.walk(dataDir)) {
            walk.sorted(Comparator.reverseOrder()).forEach(p -> {
                try {
                    Files.deleteIfExists(p);
                } catch (IOException e) {
                    throw new UncheckedIOException(e);
                }
            });
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    private void writeSymbols() {
        try {
            Files.createDirectories(dataDir);
            Files.writeString(dataDir.resolve("_symbols.json"), """
                    {"version":1,"symbols":{
                      "CME_MINI:MES1!":{"folder":"CME_MINI_MES1!","tick":0.25,"tickValue":0.5,"commission":0.5},
                      "CME_MINI:MNQ1!":{"folder":"CME_MINI_MNQ1!","tick":0.25,"tickValue":0.5,"commission":0.5}}}
                    """);
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    private void writeDay(String folder, String day, String... rows) {
        try {
            Path dir = dataDir.resolve(folder);
            Files.createDirectories(dir);
            Files.write(dir.resolve(day + ".csv"), List.of(rows));
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }
}
