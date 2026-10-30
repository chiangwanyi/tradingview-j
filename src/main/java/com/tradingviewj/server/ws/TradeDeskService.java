package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.dto.TradeView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.BarAdvancedPayload;
import com.tradingviewj.server.entity.audit.payload.VoiceState;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.service.AuditLogService;
import com.tradingviewj.server.service.BarCursorService;
import com.tradingviewj.server.service.BarQueryService;
import com.tradingviewj.server.service.PropFirmAccountService;
import com.tradingviewj.server.service.TradingService;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.math.BigDecimal;
import java.nio.file.Path;
import java.util.List;

/**
 * 交易页的<strong>业务动作</strong>：连上之后的快照，以及每一条改盘面的消息。
 *
 * <h2>它和 {@code TradeSocketHandler} / {@code TradingService} 的分工</h2>
 *
 * <ul>
 *   <li>Handler 只管<strong>连接</strong>（谁连着、广播给谁、怎么发）和<strong>解析</strong>
 *       （这段 JSON 是哪种消息）。</li>
 *   <li>{@link TradingService} 管<strong>账</strong>（下单、撮合、成交、盈亏）。</li>
 *   <li><strong>这里</strong>管<strong>盘面</strong>：连上时的快照、【下一根】的推进顺序、
 *       以及把「账」和「K 线」拼成一份广播。分开的实际好处是这一层能脱离 WebSocket 测
 *       —— 一个连不上的浏览器不会让你没法验证「步进一根」。</li>
 * </ul>
 *
 * <h2>为什么每个动作都要「先读账户再动手」</h2>
 *
 * <p>{@link PropFirmAccountService#requireActive}（下单类动作是 {@code requireTradable}）
 * 是三条业务规则的唯一落点（账户必须存在、必须还在考核中）。绕过它直接读库，
 * 一个已判失败的账户就会继续往后放 K 线、继续写流水 —— 而那些流水事后会被算进它的
 * 交易日和盈利日。
 *
 * <h2>消息一览</h2>
 *
 * <pre>
 * hello        → snapshot（盘面 + 账）        只回发起方
 * step         → bar_advanced                 广播（含本根撮合出来的成交）
 * order_placed → order_placed                 广播（挂了一张限价/止损）
 * market_order → order_filled                 广播（市价即成交，可带保护性止损）
 * order_cancelled → order_cancelled           广播
 * order_moved  → order_moved                  广播
 * flat_all     → flat_all                     广播（市价退仓 + 撤掉全部挂单）
 * </pre>
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class TradeDeskService {

    /** 语音每根的上限。和 {@code AudioClip} 的规则同一个数，前端只照它显示，不自己数。 */
    private static final int VOICE_MAX_PER_BAR = 9;

    private final PropFirmAccountService accounts;
    private final CsvBarStore bars;
    private final BarQueryService barQuery;
    private final BarCursorService cursor;
    private final AuditLogService audit;
    private final TradingService trading;

    /**
     * 账户现在的盘面（K 线窗口 + 账）。
     *
     * <p>窗口口径和 HTTP {@code /api/bars} 完全一致（上一交易日整天 + 截止那天的 {@code barTime} 之前），
     * 直接复用 {@link BarQueryService}，不再写第二份「上一交易日是哪个文件」的算法。
     *
     * <p>账户停在的那一天如果<strong>没有日文件</strong>（被清理 / 那天没抓 / 那天休市），
     * 退到磁盘上最新的一天整天。理由和交易页原来的退路一样：宁可多给一块带上下文的盘面，
     * 也不要给一块空板 —— 但「停在哪一根」不能跟着改，那是账户记着的事实。
     */
    @Transactional(readOnly = true)
    public DeskSnapshot snapshot(String accountId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        SymbolSpec spec = trading.spec(account.getSymbolId());
        String stoppedAt = account.getLastBarTime();

        BarSeriesResponse series = window(spec, stoppedAt);
        String barTime = series.bars().isEmpty()
                ? (stoppedAt == null ? "" : stoppedAt)
                : series.bars().get(series.bars().size() - 1).time();

        TradingService.Portfolio book = trading.portfolio(account);
        return DeskSnapshot.of(
                accountId,
                account.getName(),
                account.getStage() == null ? null : account.getStage().name(),
                account.getStatus() == null ? null : account.getStatus().name(),
                spec.code(),
                spec.folder(),
                spec.tick(),
                spec.tickValue(),
                series.day(),
                barTime,
                cursor.hasNext(spec.folder(), stoppedAt),
                series.bars(),
                book.orders(),
                book.trades(),
                book.closedLots(),
                book.position(),
                book.realizedPnl(),
                book.unrealizedPnl());
    }

    /**
     * 【下一根】：把盘面往后放一根，并用那根 K 线撮合挂单。
     *
     * <p>顺序是<strong>固定的</strong>，五步都不能换：
     *
     * <ol>
     *   <li>找出当前这根之后的第一根（{@link BarCursorService}，会跨日文件、会跳过空文件）；</li>
     *   <li>把账户的 {@code lastBarTime} 推过去 —— <strong>先推账户再写流水</strong>：
     *       两者的值来自同一个 {@code barTime}，但账户是「下次打开停在哪」的唯一来源，
     *       流水是「当时发生过什么」的日志。先写流水再推账户，中间失败就留下一条
     *       「记录了步进、但账户没动」的错位；反过来失败则只是少一条流水，盘面本身还是对的。</li>
     *   <li><strong>用这根 K 线撮合挂单</strong>（{@link TradingService#match}）：
     *       触发判定、跳空修正、OCO 撤销、落成交，全部发生在这一根<strong>揭示之后</strong>；
     *       成交结果直接并进下面那条 {@code bar_advanced}，不另发 {@code order_filled} ——
     *       拆成两条的话，前端会在「K 线到了、成交还没到」的那一帧画出错误的持仓线。</li>
     *   <li>写 {@code BAR_ADVANCED} 流水（payload = 广播内容，见 {@link BarAdvancedPayload}）；</li>
     *   <li>返回给 Handler 广播。</li>
     * </ol>
     *
     * <p><strong>没有下一根时抛业务异常</strong>，不返回 null：走到磁盘上最后一根是一个
     * 用户需要被告知的状态（「等再抓一段行情」），静默成功会让按钮看起来坏了。
     *
     * @throws BusinessException 账户不能操作 / 标的读不到 / 已经是最后一根
     */
    @Transactional
    public TradingService.Event step(String accountId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        SymbolSpec spec = trading.spec(account.getSymbolId());
        String stoppedAt = account.getLastBarTime();

        BarCursorService.NextBar next = cursor.next(spec.folder(), stoppedAt);
        if (next == null) {
            throw new BusinessException(stoppedAt == null || stoppedAt.isBlank()
                    ? "标的 " + spec.code() + " 还没有任何 K 线文件，放不了下一根"
                    : stoppedAt + " 已经是磁盘上最后一根 K 线了：等抓取程序再录一段才能继续");
        }

        String barTime = next.bar().barTime();
        accounts.advanceCursor(accountId, barTime);

        // 撮合在「揭示之后」：本根挂的单本根不撮合（placedAtBar 严格早于本根），
        // 所以这里只可能成交「上一根或更早」挂的单。
        List<Trade> fills = trading.match(account, spec, next.bar());
        TradingService.Portfolio book = trading.portfolio(account);

        BarAdvancedPayload payload = new BarAdvancedPayload(
                new BarView(barTime, next.bar().open(), next.bar().high(),
                        next.bar().low(), next.bar().close(), next.bar().volume()),
                next.indexInDay(),
                next.restOfDay(),
                next.hasNext(),
                book.orders(),
                book.trades(),
                book.closedLots(),
                book.position(),
                book.realizedPnl(),
                book.unrealizedPnl(),
                // fills 只装这一根触发的成交（多数时候为空），trades 才是历史全量
                fills.stream().map(TradeView::of).toList(),
                new VoiceState(barTime, List.of(), 0, VOICE_MAX_PER_BAR));

        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.debug("账户 {} 步进到 {}（seq={}，今天还剩 {} 根，本根成交 {} 笔）",
                accountId, barTime, event.getSeq(), next.restOfDay(), fills.size());
        return new TradingService.Event(accountId, payload.action(), payload, event.getSeq());
    }

    /** 挂一张限价 / 止损单。见 {@link TradingService#place}。 */
    @Transactional
    public TradingService.Event place(String accountId, String side, String kind,
                                      BigDecimal price, BigDecimal quantity) {
        return trading.place(accountId, side, kind, price, quantity);
    }

    /** 市价单（可带保护性止损）。见 {@link TradingService#market}。 */
    @Transactional
    public TradingService.Event market(String accountId, String side,
                                       BigDecimal quantity, BigDecimal stopDistance) {
        return trading.market(accountId, side, quantity, stopDistance);
    }

    /** 撤单。见 {@link TradingService#cancel}。 */
    @Transactional
    public TradingService.Event cancel(String accountId, String orderId) {
        return trading.cancel(accountId, orderId);
    }

    /** 改挂单价。见 {@link TradingService#move}。 */
    @Transactional
    public TradingService.Event move(String accountId, String orderId, BigDecimal price) {
        return trading.move(accountId, orderId, price);
    }

    /** 市价退仓 + 撤掉全部挂单。见 {@link TradingService#flatAll}。 */
    @Transactional
    public TradingService.Event flatAll(String accountId) {
        return trading.flatAll(accountId);
    }

    /**
     * 盘面窗口：{@code 上一交易日整天 → stoppedAt}。
     *
     * <p>账户停的那天没有文件时，用「磁盘上最新一天 23:59」当截止时刻 —— 整天给。
     */
    private BarSeriesResponse window(SymbolSpec spec, String stoppedAt) {
        List<Path> files = bars.dayFiles(spec.folder());
        if (files.isEmpty()) {
            throw new BusinessException("标的 " + spec.code() + " 还没有任何 K 线文件（目录 "
                    + spec.folder() + " 是空的）：先跑抓取程序录一段行情");
        }
        if (stoppedAt != null && !stoppedAt.isBlank() && hasDayFile(files, stoppedAt)) {
            return barQuery.day(spec.code(), stoppedAt);
        }
        String lastDay = dayOf(files.get(files.size() - 1));
        return barQuery.day(spec.code(), lastDay + " 23:59");
    }

    /** 这个时刻落在的那一天，磁盘上有没有对应的日文件。 */
    private static boolean hasDayFile(List<Path> files, String barTime) {
        if (barTime.length() < 10) {
            return false;
        }
        String day = barTime.substring(0, 10);
        for (Path file : files) {
            if (dayOf(file).equals(day)) {
                return true;
            }
        }
        return false;
    }

    /** {@code .../2024-03-04.csv} → {@code 2024-03-04}。 */
    private static String dayOf(Path file) {
        String name = file.getFileName().toString();
        return name.endsWith(".csv") ? name.substring(0, name.length() - 4) : name;
    }
}
