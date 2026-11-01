package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AuditEventView;
import com.tradingviewj.server.dto.ClearTradeDataResponse;
import com.tradingviewj.server.dto.PageResponse;
import com.tradingviewj.server.dto.TradeRecordView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.repository.AuditEventRepository;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRepository;
import lombok.RequiredArgsConstructor;
import org.springframework.data.domain.Page;
import org.springframework.data.domain.PageRequest;
import org.springframework.data.domain.Sort;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * 「操作记录 / 交易记录」两张列表的<strong>只读</strong>查询，外加「清空操作记录」。
 *
 * <h2>为什么两张表一个 service</h2>
 *
 * <p>它们是同一件事的两面（一个账户做了什么 / 一笔交易的结果），列表页共用分页与
 * 账户名解析这两段逻辑；拆成两个 service 就得把这两段抄两遍。
 *
 * <h2>清空的两个入口，边界不一样</h2>
 *
 * <p>{@link #clearAuditEvents()} 清的是 {@code audit_event}（「当时每一步操作」的流水）。
 *
 * <p>{@link #clearTradeData()} 清的是「盘面上到底做过什么」那一组 4 张表：
 * {@code trade_order} → {@code trade} → {@code trade_record} → {@code trade_record_tag}。
 * 两个清空<strong>互不牵连</strong>：流水没了不影响交易记录（记录锚在 {@code trade.seq} 上），
 * 交易记录没了也不影响流水。用户想「把这个库的交易痕迹全抹掉」就得两个都点。
 *
 * <h2>清空操作记录的边界</h2>
 *
 * <p>{@code audit_event} 原本是 append-only 的，这里是<strong>唯一的删除入口</strong>，
 * 由用户手动点按钮触发。它<strong>不动</strong>交易记录：{@code trade_record} 的区间
 * 锚在 {@code trade.seq} 上（2026-10-30 起），不是 {@code audit_event.seq}，
 * 所以清空之后回放照样能演，只是「当时每一步操作」的流水没了。
 *
 * <p>清空后各账户的 {@code seq} 从 0 重新开始 —— 唯一索引是
 * {@code (account_id, seq)}，表空了不会撞。
 */
@Service
@RequiredArgsConstructor
public class HistoryQueryService {

    /** 一页最多 200 条：再大就不是「翻页」而是「拉全表」，前端也画不动。 */
    private static final int MAX_PAGE_SIZE = 200;

    private final AuditEventRepository events;
    private final TradeRecordRepository records;
    private final PropFirmAccountRepository accounts;
    private final TradeRepository trades;
    private final OrderRepository orders;

    /** 操作记录：新的在前。 */
    @Transactional(readOnly = true)
    public PageResponse<AuditEventView> auditEvents(int page, int size) {
        Page<AuditEvent> found = events.findAll(page(page, size, Sort.by(Sort.Direction.DESC, "createdAt")));
        Map<String, String> names = accountNames();
        List<AuditEventView> items = found.getContent().stream()
                .map(e -> new AuditEventView(e.getId(), e.getAccountId(), names.get(e.getAccountId()),
                        e.getSymbolId(), e.getBarTime(), e.getSeq(),
                        wire(e.getActor().name()), wire(e.getAction().name()), e.getCreatedAt()))
                .toList();
        return new PageResponse<>(items, found.getTotalElements(), found.getNumber(), found.getSize());
    }

    /** 交易记录：按开仓时间倒序，还没开仓（{@code entry_bar_time} 为空）的排最后。 */
    @Transactional(readOnly = true)
    public PageResponse<TradeRecordView> tradeRecords(int page, int size) {
        Page<TradeRecord> found =
                records.findAll(page(page, size, Sort.by(Sort.Direction.DESC, "entryBarTime")));
        List<TradeRecordView> items = found.getContent().stream()
                .map(r -> new TradeRecordView(r.getId(), r.getAccountName(), r.getSymbolId(),
                        r.getSide() == null ? null : wire(r.getSide().name()),
                        r.getQuantity(), r.getEntryBarTime(), r.getExitBarTime(),
                        r.getEntryPrice(), r.getExitPrice(), r.getRealizedPnl(),
                        r.getCommission(), r.netPnl(),
                        r.isClosed(), wire(r.getActor().name())))
                .toList();
        return new PageResponse<>(items, found.getTotalElements(), found.getNumber(), found.getSize());
    }

    /** 清空全部操作记录，返回删掉几行。 */
    @Transactional
    public long clearAuditEvents() {
        long n = events.count();
        events.deleteAllInBatch();
        return n;
    }

    /**
     * 清空全部交易信息：挂单、成交、交易记录、记录标签，4 张表一起。
     *
     * <h2>顺序不能换</h2>
     *
     * <p>删除顺序是 {@code trade_record_tag} → {@code trade} → {@code trade_record}
     * → {@code trade_order}，因为 {@code trade.trade_record_id} 和
     * {@code trade_record_tag.record_id} 都指向 {@code trade_record}。
     * 反过来先删记录也不会报错（SQLite 会按 {@code ON DELETE SET NULL} / {@code CASCADE}
     * 替你收拾），但那样「删了几行标签」就只能靠数差，拿不到准数。清空是不可逆的，
     * 账要对得上，所以从叶子往根删。
     *
     * <h2>它<strong>不</strong>动什么</h2>
     *
     * <ul>
     *   <li>{@code audit_event}（操作记录）—— 那是「我按过哪些按钮」，和「手上做过哪些单」
     *       是两件事；要一起清就去点操作记录页的清空。</li>
     *   <li>{@code prop_firm_account}（账户本身，含余额 {@code balance}）。余额是<strong>落库的
     *       字面值</strong>，不由成交反推（升级时会被强制拨回账户规模，反推必错）。
     *       所以清完交易，账户余额不会跟着回退 —— 那是「重开一个账户」该做的事，
     *       不是「删几条成交」的副作用。</li>
     *   <li>图形、语音、K 线 CSV、策略 —— 各自有表，和这 4 张无关。</li>
     * </ul>
     *
     * <p>清完之后 {@code trade.seq} 从 1 重新开始：流水号取的是
     * {@code max(seq) + 1}，表空了自然归零，唯一索引 {@code (account_id, seq)} 不会撞。
     *
     * <p><strong>已经打开的交易页需要刷新</strong>：快照是连上那一刻发的，
     * 服务端不留持仓缓存（每次 {@code snapshot} 都重查这 4 张表），
     * 但浏览器里那份旧持仓和挂单线不会自己消失。
     */
    @Transactional
    public ClearTradeDataResponse clearTradeData() {
        long tags = records.deleteAllTags();
        long traded = trades.count();
        long recorded = records.count();
        long ordered = orders.count();

        trades.deleteAllInBatch();
        records.deleteAllInBatch();
        orders.deleteAllInBatch();

        return new ClearTradeDataResponse(ordered, traded, recorded, tags);
    }

    /**
     * 账户 id → 名字。
     *
     * <p>账户个数是「一个人开了几个考核号」的量级，一次全取进内存比按行 N+1 查询干净；
     * 账户已删的记录拿不到名字（{@code null}），前端显示 id 的尾巴即可。
     */
    private Map<String, String> accountNames() {
        return accounts.findAll().stream()
                .filter(a -> a.getName() != null)
                .collect(Collectors.toMap(PropFirmAccount::getId, PropFirmAccount::getName, (a, b) -> a));
    }

    private static PageRequest page(int page, int size, Sort sort) {
        return PageRequest.of(Math.max(page, 0), Math.min(Math.max(size, 1), MAX_PAGE_SIZE), sort);
    }

    /** 枚举 → 小写协议串，和 WS 广播同一个写法（{@code ORDER_PLACED} → {@code order_placed}）。 */
    private static String wire(String constant) {
        return constant.toLowerCase(Locale.ROOT);
    }
}
