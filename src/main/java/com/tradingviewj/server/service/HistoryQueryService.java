package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AuditEventView;
import com.tradingviewj.server.dto.ClearTradeDataResponse;
import com.tradingviewj.server.dto.DailyPnlView;
import com.tradingviewj.server.dto.PageResponse;
import com.tradingviewj.server.dto.TradeRecordView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.Money;
import com.tradingviewj.server.exception.BusinessException;
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

import java.math.BigDecimal;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import java.util.stream.Collectors;

/**
 * 「操作记录 / 交易记录」两张列表的<strong>只读</strong>查询，外加「清空操作记录」。
 *
 * <h2>为什么两张表一个 service</h2>
 *
 * <p>它们是同一件事的两面（一个账户做了什么 / 一笔交易的结果），列表页共用分页与
 * 账户名解析这两段逻辑；拆成两个 service 就得把这两段抄两遍。
 *
 * <p>同样只读的还有账户详情页的<strong>交易日历</strong>（{@link #dailyPnl}）：
 * 它也是 {@code trade_record} 的一层聚合，和上面那份「交易记录」共用 {@link #view} 映射。
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
        List<TradeRecordView> items = found.getContent().stream().map(HistoryQueryService::view).toList();
        return new PageResponse<>(items, found.getTotalElements(), found.getNumber(), found.getSize());
    }

    /**
     * 一个账户的<strong>交易日历</strong>：每天做了几笔、净额多少，按交易日升序。
     *
     * <h2>为什么会话在这里、而不在 {@code PropFirmAccountService}</h2>
     *
     * <p>它读的是 {@code trade_record}，和上面那份「交易记录」是同一张表、同一套映射
     * （{@link #view}）；账户服务那边没有这张表的仓储，也不该为了画日历去认识它。
     * 账户只提供 id，规则（能不能交易、余额多少）仍然只有账户服务一个出口。
     *
     * <h2>口径：交易日，不是日历日</h2>
     *
     * <p>归日走 {@link PropFirmClock#tradingDayOf(String)} —— 和日终结算、判失败、
     * 「结算今日」同一条：交易日 D = {@code [D 06:00, D+1 04:00)}。
     * 所以 10-30 凌晨 03:10 平的那笔落在 <strong>10-29</strong> 那一格。
     * 这里绝不自己拿 {@code entry_bar_time} 的前 10 个字符当日期 ——
     * 那样每个月会有四五个晚上，日历上的钱和账户余额对不上。
     *
     * <p>只统计<strong>已平仓</strong>（{@code closed = true}）的记录：还在手上的那笔
     * 归到哪一天都不对，盈亏也还在浮动。唯一例外是 {@code tradingDayOf} 返回
     * {@code null}（落在 04:00–06:00 的禁交易窗口，PF 本该拒绝这种成交）——
     * 那种数据按日历日归到当天，宁可显示得难看，也不要让它从日历上消失。
     */
    @Transactional(readOnly = true)
    public List<DailyPnlView> dailyPnl(String accountId) {
        if (!accounts.existsById(accountId)) {
            throw new BusinessException("PF 账户不存在: " + accountId);
        }
        Map<LocalDate, List<TradeRecord>> byDay = new TreeMap<>();
        for (TradeRecord r : records.findByAccountIdAndClosedTrueOrderByEntryBarTimeAsc(accountId)) {
            LocalDate day = dayOf(r);
            if (day == null) {
                continue;
            }
            byDay.computeIfAbsent(day, k -> new ArrayList<>()).add(r);
        }

        List<DailyPnlView> days = new ArrayList<>(byDay.size());
        for (Map.Entry<LocalDate, List<TradeRecord>> e : byDay.entrySet()) {
            BigDecimal gross = BigDecimal.ZERO;
            BigDecimal commission = BigDecimal.ZERO;
            int wins = 0;
            int losses = 0;
            List<TradeRecordView> items = new ArrayList<>(e.getValue().size());
            for (TradeRecord r : e.getValue()) {
                gross = gross.add(Money.round(r.getRealizedPnl()));
                commission = commission.add(Money.round(r.getCommission()));
                BigDecimal net = r.netPnl();
                if (net != null && net.signum() > 0) {
                    wins++;
                } else if (net != null && net.signum() < 0) {
                    losses++;
                }
                items.add(view(r));
            }
            days.add(new DailyPnlView(e.getKey().toString(), items.size(), wins, losses,
                    Money.round(gross.subtract(commission)), Money.round(gross),
                    Money.round(commission), List.copyOf(items)));
        }
        return days;
    }

    /** 清空全部操作记录，返回删掉几行。 */
    @Transactional
    public long clearAuditEvents() {
        long n = events.count();
        events.deleteAllInBatch();
        return n;
    }

    /**
     * 清空全部交易信息：挂单、成交、交易记录、记录标签关联，4 张表一起。
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
     * <h2>{@code trade_tag} 不在清空范围内（2026-11-01）</h2>
     *
     * <p>标签在数据库里分两张表：{@code trade_tag} 是「我有哪些标签」这本册子，
     * {@code trade_record_tag} 是「哪条记录打了哪个标签」的关联行。
     * 清空只删<strong>关联行</strong> —— 交易删光了，标签本子还在，
     * 下次做单能接着用（和「清空聊天记录不删通讯录」是同一个道理）。
     *
     * <h2>它<strong>不</strong>动什么</h2>
     *
     * <ul>
     *   <li>{@code audit_event}（操作记录）—— 那是「我按过哪些按钮」，和「手上做过哪些单」
     *       是两件事；要一起清就去点操作记录页的清空。</li>
     *   <li>{@code trade_tag}（标签字典）—— 见上。</li>
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
        long tagLinks = records.deleteAllTags();
        long traded = trades.count();
        long recorded = records.count();
        long ordered = orders.count();

        trades.deleteAllInBatch();
        records.deleteAllInBatch();
        orders.deleteAllInBatch();

        return new ClearTradeDataResponse(ordered, traded, recorded, tagLinks);
    }

    /**
     * 记录落在哪个交易日。
     *
     * <p>{@code entry_bar_time} 为空（理论上已平仓的记录不会有）→ {@code null}，调用方跳过；
     * 落在禁交易窗口 → 退化成日历日，见 {@link #dailyPnl} 的说明。
     */
    private static LocalDate dayOf(TradeRecord r) {
        String entry = r.getEntryBarTime();
        if (entry == null || entry.isBlank()) {
            return null;
        }
        LocalDate day = PropFirmClock.tradingDayOf(entry);
        if (day != null) {
            return day;
        }
        return PropFirmClock.parse(entry).toLocalDate();
    }

    /** 一条交易记录 → 列表 / 日历共用的一份摘要。两处各写一遍迟早有一处忘了扣手续费。 */
    private static TradeRecordView view(TradeRecord r) {
        return new TradeRecordView(r.getId(), r.getAccountName(), r.getSymbolId(),
                r.getSide() == null ? null : wire(r.getSide().name()),
                r.getQuantity(), r.getEntryBarTime(), r.getExitBarTime(),
                r.getEntryPrice(), r.getExitPrice(), r.getRealizedPnl(),
                r.getCommission(), r.netPnl(),
                r.isClosed(), wire(r.getActor().name()));
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
