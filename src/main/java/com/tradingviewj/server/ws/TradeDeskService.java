package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.dto.PfView;
import com.tradingviewj.server.dto.TradeView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.BarAdvancedPayload;
import com.tradingviewj.server.entity.audit.payload.FlatAllPayload;
import com.tradingviewj.server.entity.audit.payload.VoiceState;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.service.AuditLogService;
import com.tradingviewj.server.service.AnnotationService;
import com.tradingviewj.server.service.BarCursorService;
import com.tradingviewj.server.service.BarQueryService;
import com.tradingviewj.server.service.PropFirmAccountService;
import com.tradingviewj.server.service.PropFirmClock;
import com.tradingviewj.server.service.PropFirmJudge;
import com.tradingviewj.server.service.TradingService;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.math.BigDecimal;
import java.nio.file.Path;
import java.util.ArrayList;
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
 * <p>{@link PropFirmAccountService#requireExisting}（下单类动作是 {@code requireTradable}）
 * 是「账户必须存在」的唯一落点。这里刻意<strong>不按状态拦</strong>：失败的账户仍然要
 * 打得开、能看盘、能步进、能收尾，只有下单被 {@code requireTradable} 拒绝
 * （见 {@code PropFirmAccountService} 里两道闸门的分工）。
 *
 * <h2>撞线那一刻要收摊</h2>
 *
 * <p>判出失败时当根就市价平掉持仓、撤掉全部挂单（{@link TradingService#closeOut}）——
 * 收摊必须在置 FAILED <strong>之前</strong>做完，因为失败之后账户就不能再下单了，
 * 留一个仓位在账上就永远平不掉，余额会一直带着浮动盈亏漂。
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
    private final PropFirmJudge judge;
    private final CsvBarStore bars;
    private final BarQueryService barQuery;
    private final BarCursorService cursor;
    private final AuditLogService audit;
    private final TradingService trading;
    private final AnnotationService annotations;

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
        // 失败的账户也要能打开：考挂那一场是复盘素材，锁死等于把录像一起锁了
        PropFirmAccount account = accounts.requireExisting(accountId);
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
                book.unrealizedPnl(),
                // 图形按标的全取：它不挂账户，换账户做同一张图，昨天画的线还在
                annotations.list(spec.code()),
                // 只读地重放一遍拿读数：快照不开事务写账户，余额的落库交给「推进一根」和成交
                judge.view(account, judge.assess(account)));
    }

    /**
     * 【下一根】：把盘面往后放一根，并用那根 K 线撮合挂单。
     *
     * <p>顺序是<strong>固定的</strong>，六步都不能换：
     *
     * <ol>
     *   <li>找出当前这根之后的第一根（{@link BarCursorService}，会跨日文件、会跳过空文件）；</li>
     *   <li>把账户的 {@code lastBarTime} 推过去 —— <strong>先推账户再写流水</strong>：
     *       两者的值来自同一个 {@code barTime}，但账户是「下次打开停在哪」的唯一来源，
     *       流水是「当时发生过什么」的日志。先写流水再推账户，中间失败就留下一条
     *       「记录了步进、但账户没动」的错位；反过来失败则只是少一条流水，盘面本身还是对的。</li>
     *   <li><strong>用这根 K 线撮合挂单</strong>（{@link TradingService#match}）：
     *       触发判定、跳空修正、落成交，全部发生在这一根<strong>揭示之后</strong>；
     *       成交结果直接并进下面那条 {@code bar_advanced}，不另发 {@code order_filled} ——
     *       拆成两条的话，前端会在「K 线到了、成交还没到」的那一帧画出错误的持仓线。
     *       <strong>禁交易时段（04:00–06:00）跳过这一步</strong>：那一段任何成交都不许发生，
     *       改为按这一根的收盘价日终强平（{@link #sessionEndClose}）。</li>
     *   <li><strong>考核判定</strong>（{@link PropFirmJudge}）：撞线就<strong>当场收摊
     *       （市价平仓 + 撤光挂单）再判失败</strong>（终态，之后只能看）；
     *       达标就<strong>当场平仓并冻结</strong>，等用户手动升级。余额和日终峰值在这一步写回账户。</li>
     *   <li>写 {@code BAR_ADVANCED} 流水（payload = 广播内容，见 {@link BarAdvancedPayload}）；</li>
     *   <li>返回给 Handler 广播。</li>
     * </ol>
     *
     * <p><strong>没有下一根时抛业务异常</strong>，不返回 null：走到磁盘上最后一根是一个
     * 用户需要被告知的状态（「等再抓一段行情」），静默成功会让按钮看起来坏了。
     *
     * <p>⚠️ 日终强平（{@link #sessionEndClose}）发生在<strong>撮合位置、考核判定之前</strong>：
     * 那笔平仓要让 {@code judge.assess} 看见，否则这一天结算的是「平仓前」的余额，
     * 峰值和亏损线会差一整笔浮亏。
     *
     * @throws BusinessException 账户不能操作 / 标的读不到 / 已经是最后一根
     */
    @Transactional
    public TradingService.Event step(String accountId) {
        PropFirmAccount account = accounts.requireExisting(accountId);
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

        List<Trade> matched;
        if (PropFirmClock.tradable(barTime)) {
            // 撮合在「揭示之后」：本根挂的单本根不撮合（placedAtBar 严格早于本根），
            // 所以这里只可能成交「上一根或更早」挂的单。
            matched = trading.match(account, spec, next.bar());
        } else {
            // 禁止交易时段（04:00–06:00，两端都关）：这一根**一张挂单都不撮合**，
            // 哪怕它的 high / low 扫过了某张单 —— 然后当场收摊。
            matched = sessionEndClose(account, barTime);
        }
        List<TradeView> fills = new ArrayList<>(matched.size() + 1);
        for (Trade fill : matched) {
            fills.add(TradeView.of(fill));
        }

        // 考核判定：只在「推进一根」时发生（收盘余额、跨日结算、撞线都落在这里）
        PropFirmJudge.State state = judge.assess(account);
        if (state.failed() && account.isOpen()) {
            // 撞线是终态。顺序：先收摊（市价平仓 + 撤单），再置 FAILED，最后重放一遍拿余额。
            // 收摊必须在置 FAILED 之前 —— 失败之后就不能下单了，留个仓位就永远平不掉。
            TradingService.CloseOut out = trading.closeOut(account);
            if (out.closeTrade() != null) {
                // 平仓成交并进这一根：不另发 flat_all，前端就不会在「K 线到了、仓位还在」那一帧画错
                fills.add(TradeView.of(out.closeTrade()));
            }
            state = judge.assess(account);
            accounts.fail(accountId, state.session());
            // 本根 K 线照常发出去：人得看见是哪一根要了他的命
        } else if (state.frozen()) {
            // 达标即冻结：先平仓（真实考核在通过那一刻就收手），再禁新单。
            // 禁新单不写任何标志位 —— 它由 requireTradable 现算（见 PropFirmJudge#requireNotFrozen），
            // 存一个布尔列反而会出现「标志和判定不一致」的第二份说法。
            if (trading.portfolio(account).position().quantity().signum() != 0) {
                TradingService.Event flat = trading.flatAll(accountId);
                if (flat.payload() instanceof FlatAllPayload closed && closed.trade() != null) {
                    fills.add(closed.trade());
                }
            }
            state = judge.assess(account);
        }
        // 余额 / 日终峰值写回账户（这两列是缓存，真值在成交 + 规则里）
        PfView pf = judge.settle(account, state);

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
                pf,
                // fills 只装这一根触发的成交（多数时候为空），trades 才是历史全量
                fills,
                new VoiceState(barTime, List.of(), 0, VOICE_MAX_PER_BAR));

        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.debug("账户 {} 步进到 {}（seq={}，今天还剩 {} 根，本根成交 {} 笔）",
                accountId, barTime, event.getSeq(), next.restOfDay(), fills.size());
        return new TradingService.Event(accountId, payload.action(), payload, event.getSeq());
    }

    /**
     * 盘面走进禁止交易时段那一根时<strong>强制收摊</strong>：按这一根的收盘价市价平掉全部仓位、
     * 撤掉全部挂单，返回那笔平仓成交（本来就空仓无挂单时返回空）。
     *
     * <h2>为什么平仓价取「这一根」的收盘价</h2>
     *
     * <p>{@code advanceCursor} 已经先把账户推到这一根，所以 {@code trading.closeOut} 内部读到的
     * 「当前 K 线」正是它 —— 用户 2026-11-01 定的规则是「03:55 开仓、步进到 04:00 时
     * 以 <b>04:00 收盘价</b>平掉」。用 04:00 的 {@code close} 而不是新交易日的开盘价：
     * 04:00 是这一天的最后一刻，用它收摊，账就停在「这一天结束时」的价位上，
     * 04:00–06:00 那段真空里的跳空不该由用户承担。
     *
     * <h2>为什么这一根不撮合</h2>
     *
     * <p>同一根 K 线的 high / low 可能扫过某张挂了很久的止损单 —— 但这段窗口里
     * <strong>任何成交都不允许发生</strong>（规则原文：「即使 04:00 能触发某些挂单也不得成交」）。
     * 所以调用方在禁交易时段<b>根本不调</b> {@code trading.match}，这里只负责收摊。
     *
     * <h2>幂等</h2>
     *
     * <p>收摊之后再走到 04:05、06:00 这些根，账户已经是空仓 + 零挂单，
     * 这里直接返回空列表，不会重复记账、也不会凭空多出流水。
     */
    private List<Trade> sessionEndClose(PropFirmAccount account, String barTime) {
        TradingService.Portfolio book = trading.portfolio(account);
        boolean flat = book.position().quantity().signum() == 0;
        boolean noOrders = book.orders().stream().noneMatch(o -> "open".equals(o.status()));
        if (flat && noOrders) {
            return List.of();
        }
        TradingService.CloseOut out = trading.closeOut(account);
        log.info("账户 {} 走到 {}（禁止交易时段）：日终收摊 —— 平 {} 手，撤 {} 张挂单",
                account.getId(), barTime,
                out.closeTrade() == null ? "0" : out.closeTrade().getQuantity().toPlainString(),
                out.cancelledOrders());
        return out.closeTrade() == null ? List.of() : List.of(out.closeTrade());
    }

    /**
     * 【结算今日】：一步把盘面推到<strong>下一个交易日</strong>的可交易位置，让上一个交易日当场结算
     * （日终余额进峰值 → 亏损线上移）。
     *
     * <p>落点按<strong>交易日</strong>算，不是日历日：交易日 D = {@code [D 06:00, D+1 04:00)}，
     * 所以从 {@code D+1 02:00} 出发的「下一个交易日」是 {@code D+1}，不是 {@code D+2}。
     *
     * <p><strong>手上不是空的就拒绝</strong>：这一天会被整段跳过，中途每一根本该触发的止损 / 限价
     * 都不会成交，用户会以为仓位还在保护里。逐根推进走【下一根】，那个才是逐根撮合的。
     *
     * <p>整段跳过只做<strong>一次重放</strong>：{@link PropFirmJudge#assess} 会把跳过的 K 线全部喂给
     * 会话，收盘余额、撞线、达标都在那一次里判出来 —— 和连点几百次【下一根】得到的读数相同
     * （前提是空仓无挂单，见上）。
     */
    @Transactional
    public PropFirmAccount settleTradingDay(String accountId) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        SymbolSpec spec = trading.spec(account.getSymbolId());
        TradingService.Portfolio book = trading.portfolio(account);
        boolean flat = book.position().quantity().signum() == 0;
        boolean noOpenOrders = book.orders().stream().noneMatch(o -> "open".equals(o.status()));
        if (!flat || !noOpenOrders) {
            throw new BusinessException("账户「" + account.getName() + "」还有"
                    + (flat ? "挂着的单" : "持仓")
                    + "：结算今日会把这一天整段跳过，中途的止损不会触发 —— "
                    + "先退仓、撤单，或者回交易页一根一根推");
        }

        BarCursorService.NextBar next = cursor.nextTradingDay(spec.folder(), account.getLastBarTime());
        if (next == null) {
            throw new BusinessException("账户停在 " + account.getLastBarTime()
                    + "，磁盘上已经没有下一个交易日的 K 线了：等抓取程序再录一段才能继续");
        }
        String barTime = next.bar().barTime();
        accounts.advanceCursor(accountId, barTime);

        // 跨到新交易日那一下就是上一个交易日的日终结算（onBar 的跨日分支）
        PropFirmJudge.State state = judge.assess(account);
        if (state.failed()) {
            accounts.fail(accountId, state.session());
        }
        PfView pf = judge.settle(account, state);

        TradingService.Portfolio after = trading.portfolio(account);
        BarAdvancedPayload payload = new BarAdvancedPayload(
                new BarView(barTime, next.bar().open(), next.bar().high(),
                        next.bar().low(), next.bar().close(), next.bar().volume()),
                next.indexInDay(),
                next.restOfDay(),
                next.hasNext(),
                after.orders(),
                after.trades(),
                after.closedLots(),
                after.position(),
                after.realizedPnl(),
                after.unrealizedPnl(),
                pf,
                List.of(),
                new VoiceState(barTime, List.of(), 0, VOICE_MAX_PER_BAR));
        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.info("账户 {} 结算今日：直接推进到 {}（seq={}，已平仓无挂单才允许整段跳过）",
                accountId, barTime, event.getSeq());
        return account;
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
