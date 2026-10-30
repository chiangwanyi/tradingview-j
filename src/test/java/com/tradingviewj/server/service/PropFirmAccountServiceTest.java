package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PositionTier;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmStageLogRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

import javax.sql.DataSource;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.sql.Connection;
import java.sql.SQLException;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

/**
 * PF 账户的生命周期，用<strong>真实的 SQLite</strong>（临时目录里的一份库）钉住。
 *
 * <p>起点抽取器被替换成 mock：它要的是真实 K 线文件，而本类要验的是
 * 「规则快照有没有复制进来」「阶段流水有没有结掉」「删账户有没有连坐」—— 和 CSV 无关。
 * 「抽不到起点就不建账户」这一条正好用 mock 抛异常来表达。
 *
 * <p>业务库 {@code ./db/tradingview-j.db} 一次都不碰，理由和
 * {@code PropFirmTemplateServiceTest} 一样：跑测试不能悄悄改用户的账户数据。
 */
@SpringBootTest
class PropFirmAccountServiceTest {

    /** 每个账户都绑同一个标的。必须在 {@code symbol} 表里存在 —— 这一列是真外键。 */
    private static final String SYMBOL = "CME_MINI:MES1!";

    private static String testDbUrl;

    @DynamicPropertySource
    static void sqliteInTempDir(DynamicPropertyRegistry registry) {
        try {
            String dir = Files.createTempDirectory("tvj-account-").toAbsolutePath().toString();
            testDbUrl = "jdbc:sqlite:" + dir + "/test.db";
            registry.add("spring.datasource.url", () -> testDbUrl);
            registry.add("app.bar-root", () -> dir + "/data");
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    @MockitoBean
    private PropFirmStartPicker picker;

    @Autowired private PropFirmAccountService service;
    @Autowired private PropFirmTemplateService templateService;
    @Autowired private PropFirmAccountRepository accounts;
    @Autowired private PropFirmStageLogRepository logs;
    @Autowired private PropFirmTemplateRepository templateRepo;
    @Autowired private com.tradingviewj.server.repository.SymbolRepository symbols;
    @Autowired private DataSource dataSource;

    @BeforeEach
    void clean() {
        logs.deleteAll();
        accounts.deleteAll();
        templateRepo.deleteAll();
        // 账户的 symbol_id 是真外键：标的必须先存在，否则开账户会在落库时被数据库拒掉
        symbols.save(com.tradingviewj.server.entity.market.Symbol.of(
                SYMBOL, "CME_MINI_MES1!", new BigDecimal("0.50")));
        // 默认抽中一个固定起点；想验失败的用例自己再 when(...).thenThrow(...)
        when(picker.pick(anyString())).thenReturn(
                new PropFirmStartPicker.Pick("MES", "2024-03-04 09:00", "2024-03-04.csv"));
    }

    /** 临时库和业务库是两份文件。 */
    @Test
    void testsNeverTouchTheBusinessDatabase() throws SQLException {
        try (Connection connection = dataSource.getConnection()) {
            assertEquals(testDbUrl, connection.getMetaData().getURL());
            assertTrue(!testDbUrl.replace('\\', '/').contains("/db/tradingview-j.db"),
                    "测试库不能是业务库: " + testDbUrl);
        }
    }

    /** 建户：规则在这一刻复制进来、余额 = 账户规模、流水第一行 seq 0 CREATED。 */
    @Test
    void openCopiesRulesSnapshotAndWritesFirstStageLog() {
        PropFirmTemplate eval = evalTemplate(null);

        PropFirmAccount account = service.open(eval.getId(), SYMBOL, "LucidFlex 50K 第一次");

        assertNotNull(account.getId());
        assertEquals("LucidFlex 50K 第一次", account.getName());
        assertEquals(eval.getId(), account.getTemplateId());
        assertEquals(PropFirmStage.EVAL, account.getStage());
        assertEquals(PropFirmAccount.Status.ACTIVE, account.getStatus());
        assertEquals(new BigDecimal("50000.00"), account.getBalance());
        assertEquals(new BigDecimal("50000.00"), account.getBaseBalance());
        assertEquals(new BigDecimal("50000.00"), account.getEodPeakBalance());
        assertEquals("2024-03-04 09:00", account.getStartBarTime());
        assertEquals("2024-03-04.csv", account.getStartDayFile());
        assertEquals("2024-03-04 09:00", account.getLastBarTime(),
                "最新时间初值就是起点：第一次「继续交易」只揭示到这一根");
        assertEquals(SYMBOL, account.getSymbolId(), "标的在建户那一刻定下，落在账户上");
        assertEquals(new BigDecimal("3000.00"), account.getRules().getProfitTarget(),
                "规则必须是副本、落在账户上，不能只靠模板");

        List<PropFirmStageLog> history = service.stageHistory(account.getId());
        assertEquals(1, history.size());
        assertEquals(0, history.get(0).getSeq());
        assertEquals(PropFirmStage.EVAL, history.get(0).getStage());
        assertEquals(PropFirmStageLog.Reason.CREATED, history.get(0).getReason());
        assertNull(history.get(0).getEndedAt(), "当前阶段还没有结束");
    }

    /** 用 FUNDED 模板直接开出来的账户，阶段就是 FUNDED，流水只有一行。 */
    @Test
    void openWithFundedTemplateStartsInFunded() {
        PropFirmTemplate funded = fundedTemplate();
        PropFirmAccount account = service.open(funded.getId(), SYMBOL, "直接买的出金号");
        assertEquals(PropFirmStage.FUNDED, account.getStage());
        assertEquals(1, service.stageHistory(account.getId()).size());
    }

    /** 模板不存在：报业务异常，库里不留半条账户。 */
    @Test
    void openWithUnknownTemplateLeavesNothing() {
        BusinessException e = assertThrows(BusinessException.class,
                () -> service.open("没有这个模板", SYMBOL, "x"));
        assertTrue(e.getMessage().contains("模板不存在"));
        assertEquals(0, accounts.count());
    }

    /** 抽不到起点：整个开账户失败，库里一条残留都没有（先抽起点、再落库）。 */
    @Test
    void openWithoutStartBarLeavesNothing() {
        PropFirmTemplate eval = evalTemplate(null);
        when(picker.pick(anyString())).thenThrow(new BusinessException("没有任何 K 线数据"));

        assertThrows(BusinessException.class, () -> service.open(eval.getId(), SYMBOL, "x"));
        assertEquals(0, accounts.count(), "抽不到起点就不能留下一个没有起点的账户");
        assertEquals(0, logs.count());
    }

    /** 没名字建不出来（实体的工厂方法把关），且不留残留。 */
    @Test
    void openRejectsBlankName() {
        PropFirmTemplate eval = evalTemplate(null);
        assertThrows(IllegalArgumentException.class, () -> service.open(eval.getId(), SYMBOL, "  "));
        assertEquals(0, accounts.count());
    }

    /** 没给标的建不出来；标的不在 symbol 表里也建不出来（抽起点那一步就拒了）。 */
    @Test
    void openRequiresAKnownSymbol() {
        PropFirmTemplate eval = evalTemplate(null);

        assertThrows(IllegalArgumentException.class, () -> service.open(eval.getId(), "  ", "x"));

        when(picker.pick("CME_MINI:XXX1!")).thenThrow(new BusinessException("不认识的标的"));
        BusinessException e = assertThrows(BusinessException.class,
                () -> service.open(eval.getId(), "CME_MINI:XXX1!", "x"));
        assertTrue(e.getMessage().contains("不认识的标的"));
        assertEquals(0, accounts.count(), "标的不合法时库里不留半条账户");
    }

    /**
     * 标的不可换：改名不会动它，阶段迁移也不会动它。
     *
     * <p>真正把「不可改」钉死的是实体上的 {@code updatable = false} + 没有 setter；
     * 这条测试钉的是「上层每一处写账户的路径都不碰它」。
     */
    @Test
    void symbolSurvivesRenameAndPromotion() {
        PropFirmTemplate funded = fundedTemplate();
        PropFirmAccount account = service.open(evalTemplate(funded.getId()).getId(), SYMBOL, "锁标的");
        String id = account.getId();

        service.rename(id, "改个名");
        assertEquals(SYMBOL, service.get(id).getSymbolId());

        service.promote(id, null);
        assertEquals(SYMBOL, service.get(id).getSymbolId(), "升级换的是规则和余额，不是标的");
    }

    /** 别的标的进不来：手数闸门和 requireSymbol 都按账户绑定的那个标的把关。 */
    @Test
    void otherSymbolsAreRejected() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "只做 MES");
        String id = account.getId();

        BusinessException e = assertThrows(BusinessException.class,
                () -> service.requireSymbol(id, "CME_MINI:MNQ1!"));
        assertTrue(e.getMessage().contains("不可切换"), e.getMessage());

        assertThrows(BusinessException.class,
                () -> service.checkPositionLimit(id, "CME_MINI:MNQ1!", BigDecimal.ONE));

        // 自己的标的照常通过（40 手上限内）
        service.checkPositionLimit(id, SYMBOL, new BigDecimal("40"));
        assertEquals(id, service.requireSymbol(id, SYMBOL).getId());
    }

    /** 改名：只换 name，身份、余额、阶段、流水都不动；空名字当场拒绝。 */
    @Test
    void renameOnlyChangesTheLabel() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "旧名字");
        String id = account.getId();
        var createdAt = service.get(id).getCreatedAt();

        PropFirmAccount renamed = service.rename(id, "2024 第一次考挂的那个");

        assertEquals(id, renamed.getId());
        assertEquals(createdAt, service.get(id).getCreatedAt());
        assertEquals(PropFirmStage.EVAL, renamed.getStage());
        assertEquals(new BigDecimal("50000.00"), renamed.getBalance());
        assertEquals(1, logs.count(), "改名不是阶段迁移，不该写流水");

        assertThrows(IllegalArgumentException.class, () -> service.rename(id, " "));
        assertEquals("2024 第一次考挂的那个", service.get(id).getName());
    }

    /** 升级：结掉旧流水、换规则、余额重置回新账户规模、开新流水。 */
    @Test
    void promoteClosesCurrentStageAndResetsBalanceToNewSize() {
        PropFirmTemplate funded = fundedTemplate();            // 账户规模 100000
        PropFirmTemplate eval = evalTemplate(funded.getId());  // 账户规模 50000
        PropFirmAccount account = service.open(eval.getId(), SYMBOL, "考过来的出金号");

        PropFirmAccount promoted = service.promote(account.getId(), null);

        assertEquals(PropFirmStage.FUNDED, promoted.getStage());
        assertEquals(funded.getId(), promoted.getTemplateId(), "模板引用跟着阶段换");
        assertEquals(new BigDecimal("100000.00"), promoted.getBalance(),
                "EVAL 的盈利不带进 FUNDED：余额重置回新阶段的账户规模");
        assertEquals(new BigDecimal("100000.00"), promoted.getEodPeakBalance());
        assertNull(promoted.getRules().getProfitTarget(), "换成了 FUNDED 那份规则快照");
        assertEquals(PropFirmAccount.Status.ACTIVE, promoted.getStatus());

        List<PropFirmStageLog> history = service.stageHistory(account.getId());
        assertEquals(2, history.size());
        PropFirmStageLog evals = history.get(0);
        assertEquals(PropFirmStageLog.EndReason.PROMOTED, evals.getEndReason());
        assertNotNull(evals.getEndedAt());
        assertEquals(0, evals.getNetPnl().compareTo(BigDecimal.ZERO),
                "结掉的那一段净额 = 结束余额 − 起点余额");
        assertEquals(1, history.get(1).getSeq());
        assertEquals(PropFirmStage.FUNDED, history.get(1).getStage());
        assertEquals(PropFirmStageLog.Reason.PROMOTED, history.get(1).getReason());
        assertNull(history.get(1).getEndedAt());
    }

    /** 模板没配 next_template_id：升不上去，账户原样不动。 */
    @Test
    void promoteWithoutNextTemplateFailsAndLeavesAccountUntouched() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "没有下一站");

        BusinessException e = assertThrows(BusinessException.class,
                () -> service.promote(account.getId(), null));
        assertTrue(e.getMessage().contains("next_template_id"));

        PropFirmAccount loaded = service.get(account.getId());
        assertEquals(PropFirmStage.EVAL, loaded.getStage());
        assertEquals(new BigDecimal("50000.00"), loaded.getBalance());
        assertEquals(1, logs.count());
        assertNull(service.stageHistory(account.getId()).get(0).getEndedAt());
    }

    /** PAYOUT 是尽头：再点一次升级必须被拒绝，绝不能把余额重置成账户规模。 */
    @Test
    void promoteFromPayoutIsRejected() {
        PropFirmTemplate funded = fundedTemplate();
        PropFirmAccount account = service.open(evalTemplate(funded.getId()).getId(), SYMBOL, "走到终点");
        service.promote(account.getId(), null);   // EVAL → FUNDED
        PropFirmAccount payout = service.promote(account.getId(), null); // FUNDED → PAYOUT
        assertEquals(PropFirmStage.PAYOUT, payout.getStage());
        assertFalse(payout.isTradable(), "PAYOUT 不再接受下单");
        assertTrue(payout.isOpen(), "但仍然 ACTIVE：看盘、标注、复盘照旧");

        BusinessException e = assertThrows(BusinessException.class,
                () -> service.promote(account.getId(), null));
        assertTrue(e.getMessage().contains("PAYOUT"));

        assertEquals(PropFirmStage.PAYOUT, service.get(account.getId()).getStage());
        assertEquals(3, logs.count(), "被拒绝的那一次不写流水");
    }

    /** 判失败是终态：之后不能再产生操作，第二次数失败直接报错。 */
    @Test
    void failIsTerminal() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "撞线了");

        PropFirmAccount failed = service.fail(account.getId(), null);
        assertEquals(PropFirmAccount.Status.FAILED, failed.getStatus());
        assertEquals(PropFirmStageLog.EndReason.FAILED,
                service.stageHistory(account.getId()).get(0).getEndReason());

        assertThrows(BusinessException.class, () -> service.requireActive(account.getId()));
        assertThrows(BusinessException.class, () -> service.requireTradable(account.getId()));
        assertThrows(BusinessException.class, () -> service.fail(account.getId(), null));
    }

    /**
     * 盘面最新时间：建户 = 起点，之后只能往后推，推到哪儿「继续交易」就从哪儿开始。
     *
     * <p>这是「重新打开交易页不能提前看到未来」的唯一依据，所以三条都钉住：
     * 初值、往后推能落库、往回退会被拒。
     */
    @Test
    void advanceCursorOnlyMovesForwardAndStartsAtTheStartBar() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "接着上次的位置");
        assertEquals("2024-03-04 09:00", account.getLastBarTime(), "建户即停在起点");

        PropFirmAccount stepped = service.advanceCursor(account.getId(), "2024-03-04 09:35");
        assertEquals("2024-03-04 09:35", stepped.getLastBarTime());
        assertEquals("2024-03-04 09:35", service.get(account.getId()).getLastBarTime(), "要真落库");

        // 同一根重复提交是幂等的（用户连点两次不该报错）
        assertEquals("2024-03-04 09:35",
                service.advanceCursor(account.getId(), "2024-03-04 09:35").getLastBarTime());

        // 往回退 = 重看已经发生的行情，账要跟着回滚，现在不做 → 拒绝，而且不改值
        assertThrows(IllegalArgumentException.class,
                () -> service.advanceCursor(account.getId(), "2024-03-04 09:30"));
        assertEquals("2024-03-04 09:35", service.get(account.getId()).getLastBarTime());

        // 格式不对也拒：时间列是「定长明文 + 字典序即时间序」，格式不齐比较就废了
        assertThrows(IllegalArgumentException.class,
                () -> service.advanceCursor(account.getId(), "2024-03-04"));
    }

    /** 考挂了的账户不能再往前走（推进盘面也是「还在考核中」才做的事）。 */
    @Test
    void advanceCursorRejectsFailedAccount() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "考挂了");
        service.fail(account.getId(), null);

        assertThrows(BusinessException.class,
                () -> service.advanceCursor(account.getId(), "2024-03-04 09:05"));
    }

    /** 删账户只删这一行：阶段流水和（将来的）成交都保留，account_id 原样留着。 */
    @Test
    void deleteRemovesOnlyTheAccountRow() {
        PropFirmTemplate funded = fundedTemplate();
        PropFirmAccount account = service.open(evalTemplate(funded.getId()).getId(), SYMBOL, "要删的");
        service.promote(account.getId(), null);
        String id = account.getId();

        service.delete(id);

        assertTrue(accounts.findById(id).isEmpty());
        assertEquals(2, logs.count(), "阶段流水是「这个账户存在过」的记录，删账户不连带删");
        assertEquals(id, service.stageHistory(id).get(0).getAccountId());

        service.delete(id); // 幂等
        assertFalse(accounts.existsById(id));
    }

    /** 读接口：不存在 → 业务异常；列表默认全部、可以按状态筛，新建的在前。 */
    @Test
    void getAndList() {
        PropFirmAccount first = service.open(evalTemplate(null).getId(), SYMBOL, "一号");
        PropFirmAccount second = service.open(fundedTemplate().getId(), SYMBOL, "二号");
        service.fail(first.getId(), null);

        BusinessException e = assertThrows(BusinessException.class, () -> service.get("没有这个账户"));
        assertTrue(e.getMessage().contains("不存在"));

        List<PropFirmAccount> all = service.list(null);
        assertEquals(2, all.size());

        List<PropFirmAccount> failedOnly = service.list(PropFirmAccount.Status.FAILED);
        assertEquals(1, failedOnly.size());
        assertEquals(PropFirmAccount.Status.FAILED, failedOnly.get(0).getStatus());

        List<PropFirmAccount> activeOnly = service.list(PropFirmAccount.Status.ACTIVE);
        assertEquals(1, activeOnly.size());
        assertEquals(second.getId(), activeOnly.get(0).getId(), "新建的在前：二号比一号晚");

        assertEquals(2, service.list(null).size(), "不传 status = 全部，失败的那些也在");
    }

    /** 会话算完写回：两个缓存列被追平，接口上立刻能读到。 */
    @Test
    void syncFromSessionWritesCachedBalanceAndPeak() {
        PropFirmAccount account = service.open(evalTemplate(null).getId(), SYMBOL, "同步一下");
        PropFirmSession session = mock(PropFirmSession.class);
        when(session.balance()).thenReturn(new BigDecimal("51234.56"));
        when(session.eodPeak()).thenReturn(new BigDecimal("51500.00"));

        service.syncFromSession(account.getId(), session);

        PropFirmAccount loaded = service.get(account.getId());
        assertEquals(new BigDecimal("51234.56"), loaded.getBalance());
        assertEquals(new BigDecimal("51500.00"), loaded.getEodPeakBalance());
        assertEquals(new BigDecimal("1234.56"), loaded.netProfitInStage());
    }

    // ---- 夹具 ----

    /** EVAL 模板：规模 50000、目标 3000、回撤 2000、一致性 0.5。 */
    private PropFirmTemplate evalTemplate(String nextTemplateId) {
        return templateService.create("LucidFlex 50K EVAL", "考试", PropFirmStage.EVAL,
                nextTemplateId, new PropFirmRules(new BigDecimal("50000.00"),
                        new BigDecimal("3000.00"), new BigDecimal("2000.00"),
                        PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100.00"),
                        new BigDecimal("0.50"), null, null,
                        List.of(new PositionTier(BigDecimal.ZERO, 40))));
    }

    /** FUNDED 模板：规模刻意用 100000，和 EVAL 的 50000 拉开，升级时「余额重置」才看得出来。 */
    private PropFirmTemplate fundedTemplate() {
        return templateService.create("LucidFlex 100K FUNDED", "出金号", PropFirmStage.FUNDED,
                null, new PropFirmRules(new BigDecimal("100000.00"), null,
                        new BigDecimal("2000.00"), PropFirmRules.DrawdownType.TRAILING_EOD,
                        new BigDecimal("102100.00"), null, 5, new BigDecimal("150.00"),
                        List.of(new PositionTier(BigDecimal.ZERO, 20))));
    }
}
