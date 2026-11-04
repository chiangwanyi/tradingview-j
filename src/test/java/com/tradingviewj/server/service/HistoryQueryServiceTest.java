package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AuditEventView;
import com.tradingviewj.server.dto.ClearTradeDataResponse;
import com.tradingviewj.server.dto.DailyPnlView;
import com.tradingviewj.server.dto.TradeRecordView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRepository;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.mockito.InOrder;
import org.springframework.data.domain.PageImpl;
import org.springframework.data.domain.PageRequest;
import org.springframework.data.domain.Pageable;

import java.math.BigDecimal;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.inOrder;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

/**
 * 历史记录查询的口径：枚举转小写协议串、账户名现查、分页参数夹紧、两个清空各自返回什么。
 *
 * <p>不连库：这两件事（怎么翻页、怎么映射字段）都和数据库无关，
 * 真库上是「JPA 排序对不对」，那种事靠跑起来看，不靠单测编。
 */
class HistoryQueryServiceTest {

    private final AuditEventRepository events = mock(AuditEventRepository.class);
    private final TradeRecordRepository records = mock(TradeRecordRepository.class);
    private final PropFirmAccountRepository accounts = mock(PropFirmAccountRepository.class);
    private final TradeRepository trades = mock(TradeRepository.class);
    private final OrderRepository orders = mock(OrderRepository.class);
    private final HistoryQueryService history =
            new HistoryQueryService(events, records, accounts, trades, orders);

    @Test
    void auditEventsMapsEnumsToWireAndClampsPageSize() {
        AuditEvent event = AuditEvent.of("acc-1", "CME_MINI:MES1!", "2026-10-30 09:05", 7,
                Actor.HUMAN, ReplayAction.ORDER_PLACED, "{}");
        when(events.findAll(any(Pageable.class)))
                .thenReturn(new PageImpl<>(List.of(event), PageRequest.of(0, 50), 1));
        PropFirmAccount acc = account("acc-1", "5 万考核号");
        when(accounts.findAll()).thenReturn(List.of(acc));

        AuditEventView row = history.auditEvents(-1, 5000).items().get(0);

        assertEquals("order_placed", row.action(), "库里存常量名，出口是小写协议串");
        assertEquals("human", row.actor());
        assertEquals("5 万考核号", row.accountName(), "账户名要现查，不是从流水里读");

        ArgumentCaptor<Pageable> captor = ArgumentCaptor.forClass(Pageable.class);
        verify(events).findAll(captor.capture());
        assertEquals(0, captor.getValue().getPageNumber(), "负页码要夹到 0");
        assertEquals(200, captor.getValue().getPageSize(), "单页上限 200");
    }

    @Test
    void tradeRecordsKeepsSideLowercaseAndNullsWhenUnfilled() {
        TradeRecord open = TradeRecord.open("acc-1", "5 万考核号", "CME_MINI:MES1!",
                "2026-10-30 09:00", 3, Actor.HUMAN, "我");
        open.openPosition(Side.SELL, "2026-10-30 09:05", new BigDecimal("6500.25"), new BigDecimal("2"));
        when(records.findAll(any(Pageable.class))).thenReturn(new PageImpl<>(List.of(open)));

        TradeRecordView row = history.tradeRecords(0, 50).items().get(0);

        assertEquals("sell", row.side());
        assertEquals(new BigDecimal("6500.25"), row.entryPrice());
        assertTrue(row.exitPrice() == null && row.exitBarTime() == null, "没收尾的记录进出场要留空");
        assertEquals("human", row.actor());
    }

    @Test
    void clearAuditEventsReportsHowManyRowsWentAway() {
        when(events.count()).thenReturn(42L);

        assertEquals(42L, history.clearAuditEvents());
        verify(events).deleteAllInBatch();
    }

    /**
     * 清空交易信息：4 张表各报自己删了几行，且<strong>从叶子往根删</strong>
     * —— 标签指向记录、成交指向记录，先删根会把「删了几行标签」变成不可知的级联副作用。
     */
    @Test
    void clearTradeDataDeletesLeafFirstAndReportsPerTable() {
        when(records.deleteAllTags()).thenReturn(5);
        when(orders.count()).thenReturn(12L);
        when(trades.count()).thenReturn(8L);
        when(records.count()).thenReturn(3L);

        ClearTradeDataResponse result = history.clearTradeData();

        assertEquals(12L, result.orders(), "挂单");
        assertEquals(8L, result.trades(), "成交");
        assertEquals(3L, result.records(), "交易记录");
        assertEquals(5L, result.tagLinks(), "记录与标签的关联行数，来自原生 DELETE 的返回值");

        InOrder deleting = inOrder(records, trades, orders);
        deleting.verify(records).deleteAllTags();
        deleting.verify(trades).deleteAllInBatch();
        deleting.verify(records).deleteAllInBatch();
        deleting.verify(orders).deleteAllInBatch();
    }

    /** 清空交易信息<strong>不碰</strong>操作记录：两个入口各管各的，别顺手多删一张表。 */
    @Test
    void clearTradeDataLeavesAuditEventsAlone() {
        history.clearTradeData();

        verify(events, org.mockito.Mockito.never()).deleteAllInBatch();
    }

    /**
     * 交易日历按<strong>交易日</strong>归并：凌晨 03:10 平的那笔算前一格（{@code 10-29}），
     * 不是它自己那天的日历日 {@code 10-30}。这条要是错了，每个月的账都会差一天。
     */
    @Test
    void dailyPnlGroupsAcrossMidnightByTradingDay() {
        when(accounts.existsById("acc-1")).thenReturn(true);
        when(records.findByAccountIdAndClosedTrueOrderByEntryBarTimeAsc("acc-1")).thenReturn(List.of(
                closed("2026-10-29 09:05", "2026-10-29 10:00", "100.00", "4.00"),
                closed("2026-10-29 23:50", "2026-10-30 03:10", "-40.00", "2.00"),
                closed("2026-10-30 09:05", "2026-10-30 09:40", "25.00", "0.00")));

        List<DailyPnlView> days = history.dailyPnl("acc-1");

        assertEquals(2, days.size(), "三笔落在两个交易日上（跨零点那笔不算新的一天）");
        DailyPnlView first = days.get(0);
        assertEquals("2026-10-29", first.day());
        assertEquals(2, first.trades());
        assertEquals(1, first.wins());
        assertEquals(1, first.losses());
        assertEquals(new BigDecimal("54.00"), first.netPnl(), "净额 = 毛利 − 手续费：(100-40) − (4+2)");
        assertEquals(new BigDecimal("60.00"), first.grossPnl());
        assertEquals(new BigDecimal("6.00"), first.commission());
        assertEquals(2, first.items().size(), "点开当天能直接列出这两笔，不用再发一次请求");

        assertEquals("2026-10-30", days.get(1).day());
        assertEquals(new BigDecimal("25.00"), days.get(1).netPnl());
    }

    /** 账户不存在要报业务错，不是默默返回一份空日历（那样页面看起来像「这天没交易」）。 */
    @Test
    void dailyPnlRejectsUnknownAccount() {
        assertThrows(BusinessException.class, () -> history.dailyPnl("nope"));
    }

    /** 一条已平仓的记录：开在 entry、平在 exit，毛利 + 手续费这两个数就是日历要汇总的东西。 */
    private static TradeRecord closed(String entry, String exit, String grossPnl, String commission) {
        TradeRecord r = TradeRecord.open("acc-1", "5 万考核号", "CME_MINI:MES1!",
                entry, 1, Actor.HUMAN, "我");
        r.openPosition(Side.BUY, entry, new BigDecimal("5000.00"), new BigDecimal("1"));
        r.addCommission(new BigDecimal(commission));
        r.closeAt(exit, new BigDecimal("5010.00"), new BigDecimal(grossPnl), 2);
        return r;
    }

    private static PropFirmAccount account(String id, String name) {
        PropFirmAccount account = mock(PropFirmAccount.class);
        when(account.getId()).thenReturn(id);
        when(account.getName()).thenReturn(name);
        return account;
    }
}
