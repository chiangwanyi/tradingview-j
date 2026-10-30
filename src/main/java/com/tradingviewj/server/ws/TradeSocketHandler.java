package com.tradingviewj.server.ws;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.enums.ReplayAction;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.service.TradingService;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Component;
import org.springframework.web.socket.CloseStatus;
import org.springframework.web.socket.TextMessage;
import org.springframework.web.socket.WebSocketSession;
import org.springframework.web.socket.handler.TextWebSocketHandler;

import java.io.IOException;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * 交易页的 WebSocket 端点（{@code /ws}）。<strong>只管连接和分发，不含业务。</strong>
 *
 * <h2>消息一览</h2>
 *
 * <pre>
 * 上行                                     下行
 * {"type":"hello","accountId":"a1"}       → {"type":"snapshot", ...盘面 + 账...}      只回发起方
 * {"type":"step","accountId":"a1"}        → {"type":"event","action":"bar_advanced",
 *                                             "payload":{...含本根撮合的成交...},
 *                                             "by":"human","seq":7}                    广播给该账户全部连接
 * {"type":"order_placed", side/kind/price/quantity}
 *                                         → {"type":"event","action":"order_placed"}  广播
 * {"type":"market_order", side/quantity[/stopDistance]}
 *                                         → {"type":"event","action":"order_filled"}  广播
 * {"type":"order_cancelled","orderId":…}  → {"type":"event","action":"order_cancelled"} 广播
 * {"type":"order_moved","orderId":…,price}→ {"type":"event","action":"order_moved"}   广播
 * {"type":"flat_all","accountId":"a1"}    → {"type":"event","action":"flat_all"}      广播
 * {"type":"ping"}                         → {"type":"pong"}                           只回发起方
 * 其它 / 出错                              → {"type":"error","msg":"..."}              只回发起方
 * </pre>
 *
 * <p><strong>{@code type} 从哪来：</strong>除了 {@code hello} / {@code step} / {@code ping}
 * 三条元命令和 {@code market_order} 这一条<strong>命令层名字</strong>，其余取值一律是
 * {@link ReplayAction} 的 {@code wire()} 形式 —— 命令名与事实名是<strong>同一串</strong>
 * （{@code order_placed} 既能当「请挂单」发上来，也会作为「挂上了」广播回来）。这样上下列表
 * 共用一份枚举，不必在 {@code switch} 里再抄一遍字面量。
 *
 * <p>为什么 {@code market_order} 是例外：市价单是<strong>一条命令产出一条事实</strong>
 * （{@code order_placed} + {@code order_filled} 两件事发生在同一瞬间，而且事实名就是后者）。
 * 用 {@code order_filled} 当命令名，「成交」就同时是「请成交」和「成交了」，方向上彻底分不开；
 * 所以命令层单独给它一个名字，和 Go 版 {@code market_order} 一致。
 *
 * <h2>三个刻意的选择</h2>
 *
 * <ol>
 *   <li><strong>连上不主动发东西。</strong> 必须由前端发 {@code hello} 说出账户 id。
 *       连接本身不带身份（不查 cookie、不查 URL 参数）：这样「哪个连接是哪个账户」
 *       只有一处答案，也不会有「URL 里写了 A、消息里写了 B」的歧义。</li>
 *   <li><strong>广播包括发起方。</strong> 前端不在本地预演动作的效果，一切以广播为准
 *       —— 一份数据一个来源，慢一个往返换来永远对得上（见 {@link EventMessage}）。</li>
 *   <li><strong>不认识的消息回 error，不静默丢。</strong> 静默丢弃会让「协议字段名写错」
 *       表现成「点了没反应」，那是最难查的一类问题。</li>
 * </ol>
 *
 * <h2>广播为什么按 accountId 分组</h2>
 *
 * <p>一个账户可能同时开两个标签页（一个看盘、一个对照）。盘面被改了，两个都该跟着变。
 * 反过来，不同账户之间的盘面<strong>没有任何关系</strong>，广播过去只会让人以为自己点错了账户。
 */
@Slf4j
@Component
@RequiredArgsConstructor
public class TradeSocketHandler extends TextWebSocketHandler {

    /** 会话属性里存账户 id 的键。 */
    private static final String ATTR_ACCOUNT = "accountId";

    /**
     * 三条<strong>元命令</strong>：连接管理，不产生任何审计事实，所以 {@link ReplayAction}
     * 里没有它们，只能在这里各命名一次。
     *
     * <p>{@code order_placed} / {@code order_cancelled} / {@code order_moved} / {@code flat_all}
     * 则以 {@code ReplayAction} 的 {@code wire()} 形式出现 —— 一份名字，两个方向共用
     * （前端 {@code webapp/src/types/protocol.ts} 的 {@code ACTION} 表是同一批串）。
     * 唯一例外是 {@code market_order}：市价单一条命令产出一条事实（{@code order_filled}），
     * 命令名不等于事实名，见类注释。
     */
    private static final String TYPE_HELLO = "hello";
    /** 推进一根。名字不等于它产出的事实（{@code bar_advanced}），这是本协议第二个例外。 */
    private static final String TYPE_STEP = "step";
    private static final String TYPE_PING = "ping";
    private static final String TYPE_PONG = "pong";
    /** 市价单：下单即成交，产出 {@code order_filled}。 */
    private static final String TYPE_MARKET_ORDER = "market_order";

    private final ObjectMapper json;
    private final TradeDeskService desk;

    /** accountId → 这个账户的所有连接。 */
    private final Map<String, Set<WebSocketSession>> rooms = new ConcurrentHashMap<>();

    @Override
    public void afterConnectionEstablished(WebSocketSession session) {
        log.debug("WS 连接建立: {}", session.getId());
    }

    @Override
    protected void handleTextMessage(WebSocketSession session, TextMessage message) {
        WsRequest request;
        try {
            request = json.readValue(message.getPayload(), WsRequest.class);
        } catch (Exception e) {
            // 解析失败要连原文一起报，否则「看不懂」这三个字帮不上任何忙
            sendTo(session, error("这条消息不是合法 JSON：" + brief(message.getPayload())));
            return;
        }
        if (request.type() == null || request.type().isBlank()) {
            sendTo(session, error("消息缺少 type 字段"));
            return;
        }

        try {
            switch (request.type()) {
                case TYPE_HELLO -> hello(session, request);
                case TYPE_STEP -> broadcast(desk.step(requireInAccount(session, request)));
                case TYPE_MARKET_ORDER -> broadcast(desk.market(
                        requireInAccount(session, request),
                        request.side(), request.quantity(), request.stopDistance()));
                case "order_placed" -> broadcast(desk.place(
                        requireInAccount(session, request),
                        request.side(), request.kind(), request.price(), request.quantity()));
                case "order_cancelled" -> broadcast(desk.cancel(
                        requireInAccount(session, request), request.orderId()));
                case "order_moved" -> broadcast(desk.move(
                        requireInAccount(session, request), request.orderId(), request.price()));
                case "flat_all" -> broadcast(desk.flatAll(requireInAccount(session, request)));
                case TYPE_PING -> sendTo(session, new Pong(TYPE_PONG));
                // 其余一律拒。判据不在这里抄一份名字清单，而是问 ReplayAction：
                // 「这是不是一个有名字的动作」。是 → 说明动作定义了但还没接通；
                // 不是 → 说明字段名写错了。两种错给两种提示，这才是能帮上忙的报错。
                default -> reject(session, request.type());
            }
        } catch (BusinessException e) {
            // 业务异常是「用户做错了什么」的正常反馈（最后一根、账户已失败…），不是故障
            sendTo(session, error(e.getMessage()));
        } catch (IllegalArgumentException | IllegalStateException e) {
            sendTo(session, error(e.getMessage() == null ? "这一步不成立" : e.getMessage()));
        } catch (Exception e) {
            log.error("处理消息 {} 失败", request.type(), e);
            sendTo(session, error("服务端处理出错：" + e.getClass().getSimpleName()));
        }
    }

    /**
     * 拒绝一条不在这条通道上的消息。
     *
     * <p>分两种说法，因为两个原因需要两种修法：名字在 {@link ReplayAction} 里 —— 动作是对的，
     * 只是这条通道还没接（改后端）；名字不在里面 —— 多半是前端字段名拼错（改前端）。
     * 合成一句「不认识」会让人两边都查一遍。
     */
    private void reject(WebSocketSession session, String type) {
        if (ReplayAction.ofWire(type).isPresent()) {
            sendTo(session, error("「" + type + "」还没接通：这条通道目前只有【下一根】和下单类消息"));
        } else {
            sendTo(session, error("不认识的消息类型: " + type));
        }
    }

    /** {@code hello}：绑定账户 + 把这块盘面整份发回去。 */
    private void hello(WebSocketSession session, WsRequest request) {
        String accountId = requireAccountId(request);
        DeskSnapshot snapshot = desk.snapshot(accountId);

        leave(session);
        session.getAttributes().put(ATTR_ACCOUNT, accountId);
        rooms.computeIfAbsent(accountId, id -> ConcurrentHashMap.newKeySet()).add(session);

        log.debug("WS {} 进入账户 {}（停在 {}，还有下一根: {}）",
                session.getId(), accountId, snapshot.barTime(), snapshot.hasNext());
        sendTo(session, snapshot);
    }

    /**
     * 做账之前先确认「这条连接已经进了这个账户」。
     *
     * <p>顺序不能省：没有 hello 就下单，消息会被<strong>广播到一个空房间</strong> ——
     * 库里写进去了、自己却什么都看不见，最难查。要求先 hello 让「哪个连接是哪个账户」
     * 永远只有一处答案。
     */
    private String requireInAccount(WebSocketSession session, WsRequest request) {
        String accountId = requireAccountId(request);
        if (!accountId.equals(session.getAttributes().get(ATTR_ACCOUNT))) {
            throw new BusinessException("还没有进入账户 " + accountId + "：先发一条 hello");
        }
        return accountId;
    }

    /**
     * 把一次动作的结果广播给该账户的全部连接（<strong>含发起方</strong>）。
     *
     * <p>不在本地先改一遍再等广播回来：那样要维护两份推算，慢一个往返换来的是永远对得上。
     */
    private void broadcast(TradingService.Event result) {
        EventMessage event = EventMessage.of(
                result.action().wire(), result.payload(),
                Actor.HUMAN.name().toLowerCase(java.util.Locale.ROOT), result.seq());
        Set<WebSocketSession> sessions = rooms.get(result.accountId());
        if (sessions == null) {
            return;
        }
        for (WebSocketSession session : sessions) {
            sendTo(session, event);
        }
    }

    @Override
    public void afterConnectionClosed(WebSocketSession session, CloseStatus status) {
        leave(session);
        log.debug("WS 连接关闭: {} ({})", session.getId(), status);
    }

    @Override
    public void handleTransportError(WebSocketSession session, Throwable exception) {
        log.warn("WS 传输错误 {}: {}", session.getId(), exception.getMessage());
        leave(session);
    }

    /** 把这条连接从它所在的房间里摘掉（关连接、重复 hello 都要走）。 */
    private void leave(WebSocketSession session) {
        Object accountId = session.getAttributes().get(ATTR_ACCOUNT);
        if (accountId == null) {
            return;
        }
        Set<WebSocketSession> sessions = rooms.get(accountId.toString());
        if (sessions != null) {
            sessions.remove(session);
            if (sessions.isEmpty()) {
                rooms.remove(accountId.toString());
            }
        }
        session.getAttributes().remove(ATTR_ACCOUNT);
    }

    private static String requireAccountId(WsRequest request) {
        if (request.accountId() == null || request.accountId().isBlank()) {
            throw new BusinessException("这条消息必须带 accountId");
        }
        return request.accountId();
    }

    /**
     * 发一条下行消息。
     *
     * <p>{@code synchronized (session)}：Spring 的 {@code WebSocketSession} <strong>不允许并发发送</strong>
     * （Tomcat 会抛 {@code IllegalStateException: TEXT_PARTIAL_WRITING}）。两个标签页同时步进时，
     * 广播循环和单个回执就可能撞在一起，所以每条连接一把锁。
     */
    private void sendTo(WebSocketSession session, Object message) {
        String text;
        try {
            text = json.writeValueAsString(message);
        } catch (Exception e) {
            log.error("下行消息序列化失败: {}", message.getClass().getSimpleName(), e);
            return;
        }
        if (!session.isOpen()) {
            return;
        }
        try {
            synchronized (session) {
                session.sendMessage(new TextMessage(text));
            }
        } catch (IOException | IllegalStateException e) {
            log.warn("发送失败（连接可能已断）{}: {}", session.getId(), e.getMessage());
        }
    }

    /** 原文只截前 120 字：一条错消息可能很大（录屏数据之类），日志里不需要整份。 */
    private static String brief(String payload) {
        if (payload == null) {
            return "(空)";
        }
        return payload.length() <= 120 ? payload : payload.substring(0, 120) + "…";
    }

    private static ErrorMessage error(String msg) {
        return new ErrorMessage("error", msg);
    }

    /** 下行的失败回执，只发给发起方。 */
    record ErrorMessage(String type, String msg) {
    }

    /** 心跳回执。 */
    record Pong(String type) {
    }
}
