package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.ClosedLotView;
import com.tradingviewj.server.dto.OrderView;
import com.tradingviewj.server.dto.PositionView;
import com.tradingviewj.server.dto.TradeView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.FlatAllPayload;
import com.tradingviewj.server.entity.audit.payload.OrderCancelledPayload;
import com.tradingviewj.server.entity.audit.payload.OrderFilledPayload;
import com.tradingviewj.server.entity.audit.payload.OrderMovedPayload;
import com.tradingviewj.server.entity.audit.payload.OrderPlacedPayload;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.record.TradeRecord;
import com.tradingviewj.server.entity.trade.TradeOrder;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.OrderRepository;
import com.tradingviewj.server.repository.TradeRecordRepository;
import com.tradingviewj.server.repository.TradeRepository;
import com.tradingviewj.server.storage.Bar;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolCatalog;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.math.BigDecimal;
import java.time.LocalDate;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.List;

/**
 * 交易台<strong>账务侧</strong>：下单、撤单、改价、退仓、撮合、以及「这一刻账上是什么样」。
 *
 * <h2>它和另外两个类怎么分工</h2>
 *
 * <ul>
 *   <li>{@link TradingEngine} —— <strong>规则</strong>：什么价触发、成交价怎么修正、
 *       同组谁成交、价格怎么吸到 tick 上。纯函数，不碰库。</li>
 *   <li><strong>这里</strong> —— <strong>编排</strong>：读账户 → 读盘面 → 校验 → 落库 → 写流水。
 *       有严格先后依赖，失败要整体回滚，所以是同步调用而不是事件。</li>
 *   <li>{@code ws.TradeDeskService} —— <strong>广播</strong>：把这里返回的负载包成一条下行消息，
 *       加上「推进一根」这种属于盘面而不是属于账务的动作。</li>
 * </ul>
 *
 * <h2>三条贯穿全类的约定</h2>
 *
 * <ol>
 *   <li><strong>账只有一份</strong>：持仓、已实现盈亏、逐批平仓全部由 {@link TradeLedger}
 *       从 {@code trade} 表重放得出，这里不存任何派生值。存一份副本就一定会有一天和成交对不上，
 *       而那时没有任何办法判断谁对。</li>
 *   <li><strong>手续费在成交那一刻快照</strong>：{@code trade.commission} 记下当时每手每边的费率 × 手数，
 *       之后改标的费率不影响历史账。净额 = 毛利 − 手续费，这就是广播里的 {@code realizedPnl}。</li>
 *   <li><strong>前端不算钱</strong>：所有金额都按协议形状（{@link TradeView} 等）算好了发出去，
 *       前端只排版。前端一旦自己算，就会出现两份口径，而两份口径迟早不一样。</li>
 * </ol>
 *
 * <h2>并发</h2>
 *
 * <p>不做乐观锁，也不按账户加锁：{@code spring.datasource.hikari.maximum-pool-size = 1}，
 * SQLite 同一时刻只有一个写者，一次「读挂单 → 算成交 → 写回」整段在同一个事务里持有连接，
 * 第二个请求在连接池上排队。两个标签页同时点，结果是<strong>串行</strong>而不是互相覆盖。
 * 这条前提写在 {@code application.yml} 里，改连接池之前先回来看这里。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class TradingService {

    private final PropFirmAccountService accounts;
    private final SymbolCatalog catalog;
    private final CsvBarStore bars;
    private final OrderRepository orderRepo;
    private final TradeRepository tradeRepo;
    private final TradeRecordRepository recordRepo;
    private final AuditLogService audit;

    /**
     * 一次动作的结果，交给 WebSocket 层广播。
     *
     * @param action  {@link ReplayAction}，发送时用 {@code wire()} 转成小写协议串
     * @param payload 和落进 {@code audit_event.payload_json} 的那份<strong>完全一致</strong>
     * @param seq     这条流水的账户内序号
     */
    public record Event(String accountId, ReplayAction action, Object payload, int seq) {
    }

    /**
     * 「此刻的账」：广播和快照共用的一份只读视图。
     *
     * @param realizedPnl   <strong>净</strong>已实现盈亏（Σ 毛利 − Σ 手续费）
     * @param unrealizedPnl 按当前这根 K 线收盘价算的浮动盈亏（没有 K 线时为 0）
     */
    public record Portfolio(
            List<OrderView> orders,
            List<TradeView> trades,
            List<ClosedLotView> closedLots,
            PositionView position,
            BigDecimal realizedPnl,
            BigDecimal unrealizedPnl
    ) {
        /** 什么都没发生过的账户。数组而不是 null：前端读 {@code .length} 时不会炸。 */
        public static Portfolio empty() {
            return new Portfolio(List.of(), List.of(), List.of(), PositionView.FLAT,
                    BigDecimal.ZERO, BigDecimal.ZERO);
        }
    }

    // ------------------------------------------------------------------
    // 读
    // ------------------------------------------------------------------

    /**
     * 某个账户此刻的账。交易页连上时的快照、以及每条单据类广播都用它。
     *
     * <p>没有仓位、没有成交时返回空数组而不是 null / 省略字段：前端拿到的一定是
     * 「确实是空的」，不是「没给我」。
     */
    @Transactional(readOnly = true)
    public Portfolio portfolio(String accountId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        return portfolio(account);
    }

    /** 同上，账户已经在手上时少一次查询（事务内部调用用这个）。 */
    public Portfolio portfolio(PropFirmAccount account) {
        SymbolSpec spec = spec(account.getSymbolId());
        List<Trade> history = tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(account.getId());
        return book(account, spec, history);
    }

    /** 挂单全量（含已成交 / 已撤销），按挂单时间升序。 */
    public List<OrderView> orders(String accountId) {
        List<TradeOrder> list = orderRepo.findByAccountIdOrderByPlacedAtBarAsc(accountId);
        List<OrderView> out = new ArrayList<>(list.size());
        for (TradeOrder order : list) {
            out.add(OrderView.of(order));
        }
        return out;
    }

    // ------------------------------------------------------------------
    // 写：挂单 / 市价 / 撤单 / 改价 / 退仓
    // ------------------------------------------------------------------

    /**
     * 挂一张限价单或止损单。
     *
     * <p>挂单本身<strong>不动账</strong>：它只是「等价格过来」的意图，成交要等后面某一根 K 线的
     * high/low 碰到它。所以这里只写 {@code trade_order} 一行 + 一条 {@code ORDER_PLACED} 流水，
     * 不产生 {@code trade}，也不改持仓。
     *
     * <p><strong>本根挂的单从下一根才撮合</strong>：{@code placedAtBar} 记下的是「下单那一刻盘面停在哪」，
     * 而撮合只认「挂单时间严格早于本根时间」（见 {@link TradingEngine#eligible}）。
     * 不这样拦，下单时那根 K 线的高低点已经可见了，等于用已收完的行情倒推成交。
     *
     * @throws BusinessException 账户已不能交易 / 参数不合法 / 标的读不到
     */
    @Transactional
    public Event place(String accountId, String sideWire, String kindWire,
                       BigDecimal price, BigDecimal quantity) {
        PropFirmAccount account = accounts.requireTradable(accountId);
        SymbolSpec spec = spec(account.getSymbolId());
        Side side = TradingEngine.parseSide(sideWire);
        TradeOrder.Type type = TradingEngine.parsePlacementType(kindWire);
        BigDecimal limitPrice = TradingEngine.normalizePrice(price, spec.tick());
        int qty = TradingEngine.normalizeQuantity(quantity);
        String barTime = requireBarTime(account);

        TradeOrder order = TradeOrder.place(accountId, spec.code(), side, type, limitPrice,
                BigDecimal.valueOf(qty), barTime);
        orderRepo.save(order);

        OrderPlacedPayload payload = new OrderPlacedPayload(orders(accountId));
        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.info("账户 {} 挂单 {} {} {} 手 @ {}（seq={}）",
                accountId, side, type, qty, limitPrice, event.getSeq());
        return new Event(accountId, payload.action(), payload, event.getSeq());
    }

    /**
     * 市价单：按<strong>当前这根 K 线（账户停的那根）的收盘价</strong>立即成交。
     *
     * <p>复盘里「现在」就是最后一根已揭示 K 线的收盘：没有盘口、没有滑点，成交价和下单框上
     * 显示的「现价」是同一个数。它和挂单走同一条状态机（{@code place} 出来是 OPEN，
     * 紧接着 {@code markFilled}），所以「一张单怎么变成已成交」只有一种写法。
     *
     * <p>{@code stopDistance} &gt; 0 时，成交后<strong>同一个事务里</strong>顺手挂一张反方向的
     * 止损单，价格 = 成交价 ∓ 距离（买减、卖加）。为什么不拆成两条消息：
     * 两条之间必然有一帧「有仓、没止损」，而第二条被拒时仓位就真的裸着了。
     *
     * <p>止损价<strong>先验后开</strong>：算出来 ≤ 0 就直接拒绝，一个仓位都不留 ——
     * 先开仓再发现挂不上，就只能靠「再市价平掉」补救，而那次平仓本身也可能失败。
     *
     * @throws BusinessException 账户不能交易 / 盘面没有可成交的收盘价 / 止损距离非法
     */
    @Transactional
    public Event market(String accountId, String sideWire, BigDecimal quantity, BigDecimal stopDistance) {
        PropFirmAccount account = accounts.requireTradable(accountId);
        SymbolSpec spec = spec(account.getSymbolId());
        Side side = TradingEngine.parseSide(sideWire);
        int qty = TradingEngine.normalizeQuantity(quantity);

        Bar bar = currentBar(account, spec);
        BigDecimal fillPrice = TradingEngine.roundPrice(bar.close(), spec.tick());
        if (fillPrice.signum() <= 0) {
            throw new BusinessException("当前 K 线（" + bar.barTime() + "）没有可成交的收盘价，市价单下不了");
        }

        BigDecimal stopPrice = null;
        if (stopDistance != null && stopDistance.signum() > 0) {
            stopPrice = TradingEngine.roundPrice(
                    side == Side.BUY ? fillPrice.subtract(stopDistance) : fillPrice.add(stopDistance),
                    spec.tick());
            if (stopPrice.signum() <= 0) {
                throw new BusinessException("止损价算出来是 " + stopPrice.toPlainString()
                        + "（成交价 " + fillPrice.toPlainString() + "，距离 " + stopDistance.toPlainString()
                        + "），这个价格挂不了单：这笔市价单没有提交");
            }
        }

        TradeOrder order = TradeOrder.place(accountId, spec.code(), side, TradeOrder.Type.MARKET, fillPrice,
                BigDecimal.valueOf(qty), bar.barTime());
        order.markFilled(bar.barTime(), fillPrice);
        orderRepo.save(order);

        List<Trade> history = new ArrayList<>(
                tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId));
        Trade trade = recordFill(account, spec, order, fillPrice, bar.barTime(), history);

        TradeOrder stopOrder = null;
        if (stopPrice != null) {
            stopOrder = TradeOrder.place(accountId, spec.code(), side.opposite(), TradeOrder.Type.STOP,
                    stopPrice, BigDecimal.valueOf(qty), bar.barTime());
            orderRepo.save(stopOrder);
        }

        OrderFilledPayload payload = filledPayload(account, spec, order, trade, stopOrder, history);
        AuditEvent event = audit.append(accountId, spec.code(), bar.barTime(), Actor.HUMAN, payload);
        log.info("账户 {} 市价{} {} 手 @ {}（手续费 {}，seq={}）{}",
                accountId, side, qty, fillPrice, trade.getCommission(), event.getSeq(),
                stopOrder == null ? "" : "，止损挂在 " + stopPrice);
        return new Event(accountId, payload.action(), payload, event.getSeq());
    }

    /**
     * 撤一张还挂着的单。
     *
     * <p>撤单<strong>不是平仓</strong>：账面上什么都没发生，只是「等价格过来」的意图作废了。
     * 已成交的单撤不掉（那是成交，不是撤销），已撤销的再撤一次也不成立 ——
     * 两种情况都报错，而不是静默成功：静默成功会让「撤单按钮坏了」和「它本来就没了」分不清。
     *
     * <p>用 {@code requireActive} 而不是 {@code requireTradable}：PAYOUT 阶段不能再开新仓，
     * 但撤掉一张旧挂单是<strong>减少</strong>风险，没有理由拒绝。
     *
     * @throws BusinessException 账户不存在 / 单据不属于这个账户 / 单据当前不是 OPEN
     */
    @Transactional
    public Event cancel(String accountId, String orderId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        TradeOrder order = requireOrder(account, orderId);
        if (!order.isOpen()) {
            throw new BusinessException("这张单当前是 " + order.getStatus() + "，撤不了"
                    + (order.getStatus() == TradeOrder.Status.FILLED ? "（已经成交了，要减仓请下单或一键退仓）" : "（它已经撤销过了）"));
        }
        order.cancel();
        orderRepo.save(order);

        OrderCancelledPayload payload = new OrderCancelledPayload(orders(accountId));
        String barTime = requireBarTime(account);
        AuditEvent event = audit.append(accountId, order.getSymbolId(), barTime, Actor.HUMAN, payload);
        log.info("账户 {} 撤单 {}（seq={}）", accountId, orderId, event.getSeq());
        return new Event(accountId, payload.action(), payload, event.getSeq());
    }

    /**
     * 改挂单价（画布上拖动挂单线）。
     *
     * <p>只改价格，不改方向/单型/手数：那三样改了就是另一张单，应该撤了重挂 ——
     * 否则「这笔操作到底算改价还是算换单」在流水里说不清。
     *
     * <p>价格同样吸到 tick 上：拖动是像素级的，落库前必须回到可报的价格。
     *
     * @throws BusinessException 单据不存在 / 不是这个账户的 / 已不是 OPEN / 价格非法
     */
    @Transactional
    public Event move(String accountId, String orderId, BigDecimal price) {
        PropFirmAccount account = accounts.requireActive(accountId);
        TradeOrder order = requireOrder(account, orderId);
        if (!order.isOpen()) {
            throw new BusinessException("这张单当前是 " + order.getStatus() + "，不能改价");
        }
        SymbolSpec spec = spec(account.getSymbolId());
        BigDecimal newPrice = TradingEngine.normalizePrice(price, spec.tick());
        if (newPrice.compareTo(order.getPrice()) == 0) {
            // 没变就不写库、不广播：否则操作记录里会多出一条「改价 6484.00 → 6484.00」的噪音
            throw new BusinessException("价格没变（" + newPrice.toPlainString() + "），不用改");
        }
        order.setPrice(newPrice);
        orderRepo.save(order);

        OrderMovedPayload payload = new OrderMovedPayload(OrderView.of(order), orders(accountId));
        String barTime = requireBarTime(account);
        AuditEvent event = audit.append(accountId, order.getSymbolId(), barTime, Actor.HUMAN, payload);
        log.info("账户 {} 改单价 {} → {}（seq={}）", accountId, orderId, newPrice, event.getSeq());
        return new Event(accountId, payload.action(), payload, event.getSeq());
    }

    /**
     * 一键退仓：按现价平掉整个仓位，并撤掉<strong>全部</strong>挂单。
     *
     * <p>顺序是<strong>先平仓、后撤单</strong>，理由是失败时不留半截状态：平仓是唯一可能失败的
     * 一步（当前 K 线没有收盘价），它失败时挂单还没动过，整个动作等于没发生。
     * 反过来一旦平仓失败，挂单已经没了、仓还在 —— 那正是用户最不想要的状态。
     *
     * <p>平仓走的是和「市价单」完全同一条路（一笔反向 {@code Trade}），所以
     * 「一键退仓算出来的钱」和「手动平仓」不可能不一样。
     *
     * <p>空仓 + 零挂单时报错而不是静默成功：那种静默会让「按钮没反应」和
     * 「真的没事可做」分不清。
     *
     * @throws BusinessException 什么都没有可退 / 盘面没有可成交的收盘价
     */
    @Transactional
    public Event flatAll(String accountId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        SymbolSpec spec = spec(account.getSymbolId());
        List<Trade> history = new ArrayList<>(
                tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(accountId));
        Position position = TradeLedger.replay(history, pointValue(spec)).position();

        TradeOrder closeOrder = null;
        Trade closeTrade = null;
        if (!position.isFlat()) {
            Bar bar = currentBar(account, spec);
            BigDecimal price = TradingEngine.roundPrice(bar.close(), spec.tick());
            if (price.signum() <= 0) {
                throw new BusinessException("当前 K 线（" + bar.barTime() + "）没有可成交的收盘价，退不了仓");
            }
            Side side = position.quantity().signum() > 0 ? Side.SELL : Side.BUY;
            closeOrder = TradeOrder.place(accountId, spec.code(), side, TradeOrder.Type.MARKET, price,
                    position.quantity().abs(), bar.barTime());
            closeOrder.markFilled(bar.barTime(), price);
            orderRepo.save(closeOrder);
            closeTrade = recordFill(account, spec, closeOrder, price, bar.barTime(), history);
        }

        int cancelled = 0;
        for (TradeOrder order : orderRepo.findByAccountIdAndStatusOrderByPlacedAtBarAsc(
                accountId, TradeOrder.Status.OPEN)) {
            order.cancel();
            orderRepo.save(order);
            cancelled++;
        }
        if (closeOrder == null && cancelled == 0) {
            throw new BusinessException("现在既没有仓位、也没有挂单，不用退仓");
        }

        Portfolio book = book(account, spec, history);
        FlatAllPayload payload = new FlatAllPayload(
                closeOrder == null ? null : OrderView.of(closeOrder),
                closeTrade == null ? null : TradeView.of(closeTrade),
                cancelled,
                book.orders(),
                book.trades(),
                book.closedLots(),
                book.position(),
                book.realizedPnl(),
                book.unrealizedPnl());
        String barTime = requireBarTime(account);
        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.info("账户 {} 一键退仓：平 {} 手，撤 {} 张挂单（seq={}）",
                accountId, closeTrade == null ? 0 : closeTrade.getQuantity(), cancelled, event.getSeq());
        return new Event(accountId, payload.action(), payload, event.getSeq());
    }

    // ------------------------------------------------------------------
    // 撮合（由「推进一根」调用）
    // ------------------------------------------------------------------

    /**
     * 用新揭示的这根 K 线撮合挂单，落成交。
     *
     * <p><strong>没有对应的广播</strong>：成交结果直接并进那条 {@code bar_advanced}。
     * 拆成两条的话，前端会在「K 线到了、成交还没到」的那一帧画出错误的持仓线。
     *
     * <p>顺序：
     *
     * <ol>
     *   <li>取还挂着的单，筛出「本根之前挂的」（本根挂的本根不撮合）；</li>
     *   <li>逐张判触发 → 定成交价（跳空修正）→ {@code markFilled} → 落 {@code trade}；</li>
     *   <li>成交的 {@code realizedPnl} 用「成交前持仓 + 本笔成交价」现算，
     *       和 {@link TradeLedger} 重放历史用的是同一个算式。</li>
     * </ol>
     *
     * <p><strong>不处理 OCO</strong>：`webapp` 只有独立挂单，单与单之间没有组合关系。
     * 同一根里两张单都被触发就成交两张 —— 那正是用户各挂各单时的真实结果，
     * 不该被一条前端根本发不出来的规则偷偷改掉。
     *
     * <p>不做部分成交：一张挂 5 手就整张 5 手成交，不按 K 线的成交量拆。
     * 一根 5 分钟 K 线只有 O/H/L/C，拆出来的比例是编的。
     *
     * @return 这一根触发的成交（按撮合顺序），交给调用方放进 {@code bar_advanced.fills}
     */
    @Transactional
    public List<Trade> match(PropFirmAccount account, SymbolSpec spec, Bar bar) {
        List<TradeOrder> open = orderRepo.findByAccountIdAndStatusOrderByPlacedAtBarAsc(
                account.getId(), TradeOrder.Status.OPEN);
        List<TradeOrder> candidates = TradingEngine.eligible(open, bar.barTime());
        if (candidates.isEmpty()) {
            return List.of();
        }

        List<Trade> history = new ArrayList<>(
                tradeRepo.findByAccountIdOrderByBarTimeAscSeqAsc(account.getId()));
        List<Trade> fills = new ArrayList<>();

        for (TradeOrder order : candidates) {
            if (!TradingEngine.triggered(order, bar)) {
                continue;
            }
            BigDecimal price = TradingEngine.fillPrice(order, bar, spec.tick());
            order.markFilled(bar.barTime(), price);
            orderRepo.save(order);
            Trade trade = recordFill(account, spec, order, price, bar.barTime(), history);
            fills.add(trade);
            log.debug("账户 {} 挂单 {} 在 {} 成交 {} 手 @ {}（毛利 {}）",
                    account.getId(), order.getId(), bar.barTime(),
                    order.getQuantity(), price, trade.getRealizedPnl());
        }
        return fills;
    }

    // ------------------------------------------------------------------
    // 内部
    // ------------------------------------------------------------------

    /**
     * 记一笔成交并落库。
     *
     * <p>{@code seq} 取「该账户已有最大 + 1」：同一根 K 线上可以有多笔成交（止损平仓 + 反手开仓、
     * 两张挂单同时触发），按 {@code barTime} 排分不出先后，按主键（UUID）排等于随机排 ——
     * 均价和已实现盈亏会在反手、部分平仓这些边界上算错，而且每次重启错得还不一样。
     *
     * <p>{@code history} 由调用方持有并<strong>原地追加</strong>：一次撮合里可能连续成交几张，
     * 后一张的持仓必须是前一张算完之后的。每次都回库重查一遍既慢又容易读到旧快照。
     */
    private Trade recordFill(PropFirmAccount account, SymbolSpec spec, TradeOrder order,
                             BigDecimal price, String barTime, List<Trade> history) {
        BigDecimal pv = pointValue(spec);
        Position before = TradeLedger.replay(history, pv).position();
        BigDecimal signed = order.getSide() == Side.BUY
                ? order.getQuantity()
                : order.getQuantity().negate();
        BigDecimal realized = TradeLedger.realizedFor(before, price, signed, pv);
        int seq = 0;
        for (Trade trade : history) {
            seq = Math.max(seq, trade.getSeq());
        }
        // 手续费 = 每手每边 × 手数；开仓和平仓各收一次，往返就是两次本字段（不是这里乘 2）。
        // 过 Money.round：手数有四位小数时 0.5 × 0.3333 = 0.16665 会算出亚分，落库前收敛到分。
        BigDecimal commission = TradeLedger.money(spec.commission().multiply(order.getQuantity()));
        Trade trade = Trade.fill(account.getId(), spec.code(), order.getId(), barTime, seq + 1,
                order.getSide(), price, order.getQuantity(), realized, commission);
        Trade saved = tradeRepo.save(trade);
        history.add(saved);
        keepRecord(account, before, signed, saved);
        return saved;
    }

    /**
     * 把交易记录的<strong>开 / 闭</strong>对齐到「这笔成交之后的净头寸」。
     *
     * <p>唯一的判据是<strong>成交后净头寸有没有穿越 0</strong>，不是「哪个按钮被点了」——
     * 一键退仓（{@link #flatAll}）的平仓和手动市价平仓是同一笔反向成交，走的就是这里，
     * 所以「主动退仓归零」不需要单独一条分支。多一条分支就多一个分叉点。
     *
     * <p>四种情形：
     *
     * <ul>
     *   <li><strong>空仓 → 有仓</strong>：开一条新记录，起点是这笔成交的 {@code seq}；</li>
     *   <li><strong>同号</strong>（加仓 / 部分平仓）：只刷新手数，起点和开仓价不动；</li>
     *   <li><strong>净归零</strong>：旧记录在这笔成交上封口；</li>
     *   <li><strong>反手</strong>：穿零是中间态，旧记录封口 + 新记录开盘，两条记录的区间在这
     *       一笔成交的 {@code seq} 上<strong>相邻共享</strong>——一笔成交在事实上就是
     *       「平掉旧仓 + 开新仓」两条腿，旧腿的盈亏 {@code realizedFor} 已经只算平仓那部分，
     *       所以整笔的 {@code realizedPnl} 全额归旧记录是对的。</li>
     * </ul>
     *
     * <p>区间锚在 {@code trade.seq}（成交流水号）而不是 {@code audit_event.seq}：撮合成交并进
     * {@code bar_advanced}、没有自己的流水行，两个 seq 序列本来就不对齐，用事件 seq 切不到
     * 成交粒度。{@code trade.seq} 每笔成交必有且账户内单调递增，正是「第几笔」的答案。
     *
     * <p>{@code Actor} 一律记 HUMAN：本类所有入口都是真人操作（机器人路径不走这里）。
     */
    private void keepRecord(PropFirmAccount account, Position before, BigDecimal signed, Trade trade) {
        BigDecimal after = before.quantity().add(signed);
        TradeRecord open = recordRepo
                .findFirstByAccountIdAndClosedFalseOrderByStartSeqAsc(account.getId())
                .orElse(null);
        if (open != null && !before.isFlat() && after.signum() != before.quantity().signum()) {
            // 反手时这笔成交的一半是平仓、一半是开仓；realizedFor 只算平仓那半，整笔全额归旧记录
            BigDecimal booked = open.getRealizedPnl() == null ? BigDecimal.ZERO : open.getRealizedPnl();
            open.closeAt(trade.getBarTime(), trade.getPrice(),
                    booked.add(trade.getRealizedPnl()), trade.getSeq());
            // 必须在这里落库：Hibernate 同一次 flush 里 insert 排在 update 之前，
            // 不先写下去，下面新插的那条会撞上 ux_record_open（那一刻旧记录还是 closed=0）
            recordRepo.saveAndFlush(open);
            open = null;
        }
        if (after.signum() == 0) {
            // 归零且原本就没有未闭合记录（手工删过 / 旧数据）：不补一条空记录。
            // 记录是统计视图，不是账 —— 账永远以 trade 表重放为准。
            return;
        }
        if (open == null) {
            open = TradeRecord.open(account.getId(), account.getName(), trade.getSymbolId(),
                    trade.getBarTime(), trade.getSeq(), Actor.HUMAN, account.getName());
            open.openPosition(after.signum() > 0 ? Side.BUY : Side.SELL,
                    trade.getBarTime(), trade.getPrice(), after.abs());
        } else {
            open.setQuantity(after.abs());
            if (open.getMaxQuantity() == null || after.abs().compareTo(open.getMaxQuantity()) > 0) {
                open.setMaxQuantity(after.abs());
            }
        }
        recordRepo.saveAndFlush(open);
    }

    /** 一条「成交了」的广播负载（市价单用）。 */
    private OrderFilledPayload filledPayload(PropFirmAccount account, SymbolSpec spec, TradeOrder order,
                                             Trade trade, TradeOrder stopOrder, List<Trade> history) {
        Portfolio book = book(account, spec, history);
        return new OrderFilledPayload(
                OrderView.of(order),
                TradeView.of(trade),
                stopOrder == null ? null : OrderView.of(stopOrder),
                book.orders(),
                book.trades(),
                book.closedLots(),
                book.position(),
                book.realizedPnl(),
                book.unrealizedPnl());
    }

    /**
     * 把一串成交流水变成一份协议视图。
     *
     * <p>{@code realizedPnl} 用<strong>净额</strong>（Σ 毛利 − Σ 手续费）：账户余额和考核判定
     * 都是这个口径，界面上「已实现盈亏」也就该是它。逐笔的毛利与手续费在 {@code trades} 里各有一份。
     */
    private Portfolio book(PropFirmAccount account, SymbolSpec spec, List<Trade> history) {
        BigDecimal pv = pointValue(spec);
        TradeLedger.Ledger ledger = TradeLedger.replay(history, pv);

        BigDecimal net = BigDecimal.ZERO;
        List<TradeView> trades = new ArrayList<>(history.size());
        for (Trade trade : history) {
            net = net.add(trade.netPnl());
            trades.add(TradeView.of(trade));
        }
        List<ClosedLotView> lots = new ArrayList<>(ledger.closedLots().size());
        for (var lot : ledger.closedLots()) {
            lots.add(ClosedLotView.of(lot));
        }
        return new Portfolio(
                orders(account.getId()),
                trades,
                lots,
                PositionView.of(ledger.position()),
                net,
                unrealized(account, spec, ledger.position(), pv));
    }

    /**
     * 浮动盈亏：{@code (现价 - 均价) × 手数 × 每点价值}（空仓时符号天然正确）。
     *
     * <p>取不到当前 K 线时返回 0，<strong>不抛异常</strong>：这条路径是「打开交易页」的，
     * 盘面数据被清理了也不该让人连持仓都看不见 —— 已实现盈亏仍然是准的。
     */
    private BigDecimal unrealized(PropFirmAccount account, SymbolSpec spec,
                                  Position position, BigDecimal pointValue) {
        if (position.isFlat()) {
            return BigDecimal.ZERO;
        }
        Bar bar = barOrNull(account, spec);
        if (bar == null) {
            return BigDecimal.ZERO;
        }
        return position.unrealizedPnl(bar.close(), pointValue);
    }

    /**
     * 当前这根（账户 {@code lastBarTime} 指向的那根）。
     *
     * <p>找不到就抛：市价单、平仓都要一个确定的价格，用「最后一根收盘价」兜底等于
     * 在盘面位置错位时悄悄按另一个价格成交。
     */
    public Bar currentBar(PropFirmAccount account, SymbolSpec spec) {
        Bar bar = barOrNull(account, spec);
        if (bar == null) {
            throw new BusinessException("账户停在 " + account.getLastBarTime()
                    + "，但磁盘上没有这一根 K 线（那天没抓 / 被清理）：先按【下一根】走到有行情的位置");
        }
        return bar;
    }

    /** 当前这根；找不到返回 null（只给「浮动盈亏」这类可降级的用途）。 */
    public Bar barOrNull(PropFirmAccount account, SymbolSpec spec) {
        String at = account.getLastBarTime();
        if (at == null || at.length() < 10) {
            return null;
        }
        LocalDate day;
        try {
            day = LocalDate.parse(at.substring(0, 10));
        } catch (DateTimeParseException e) {
            return null;
        }
        for (Bar bar : bars.readDay(spec.folder(), day)) {
            if (at.equals(bar.barTime())) {
                return bar;
            }
        }
        return null;
    }

    /** 账户绑定的标的。账户上的 {@code symbolId} 是标的<strong>代码</strong>，磁盘上是目录名。 */
    public SymbolSpec spec(String code) {
        if (code == null || code.isBlank()) {
            throw new BusinessException("这个账户没有绑定标的，无法确定看哪个品种");
        }
        List<SymbolSpec> all = catalog.read();
        for (SymbolSpec spec : all) {
            if (spec.code().equals(code)) {
                return spec;
            }
        }
        List<String> known = new ArrayList<>();
        for (SymbolSpec spec : all) {
            known.add(spec.code());
        }
        throw new BusinessException("账户的标的 " + code + " 不在标的清单里（data/_symbols.json 里没有它），"
                + "清单里只有: " + known);
    }

    /** 每点价值 = tickValue / tick；没配刻度时退化成 1（见 {@link TradeLedger#pointValue}）。 */
    public BigDecimal pointValue(SymbolSpec spec) {
        return TradeLedger.pointValue(spec.tick(), spec.tickValue());
    }

    private TradeOrder requireOrder(PropFirmAccount account, String orderId) {
        if (orderId == null || orderId.isBlank()) {
            throw new BusinessException("这条消息必须带 orderId");
        }
        TradeOrder order = orderRepo.findById(orderId)
                .orElseThrow(() -> new BusinessException("没有这张挂单: " + orderId + "（可能已经删掉或填错了）"));
        if (!account.getId().equals(order.getAccountId())) {
            throw new BusinessException("挂单 " + orderId + " 不属于账户 " + account.getId());
        }
        if (!account.tradesSymbol(order.getSymbolId())) {
            throw new BusinessException("挂单 " + orderId + " 的品种和账户绑定的不一致："
                    + order.getSymbolId() + " / " + account.getSymbolId());
        }
        return order;
    }

    private static String requireBarTime(PropFirmAccount account) {
        String at = account.getLastBarTime();
        if (at == null || at.isBlank()) {
            throw new BusinessException("账户还没有停在任何一根 K 线上，无法记录这次操作");
        }
        return at;
    }
}
