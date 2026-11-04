package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AnnotationPointView;
import com.tradingviewj.server.dto.AnnotationStyleView;
import com.tradingviewj.server.dto.AnnotationView;
import com.tradingviewj.server.entity.audit.payload.AnnotationAddedPayload;
import com.tradingviewj.server.entity.audit.payload.AnnotationClearedPayload;
import com.tradingviewj.server.entity.audit.payload.AnnotationDeletedPayload;
import com.tradingviewj.server.entity.audit.payload.AnnotationMovedPayload;
import com.tradingviewj.server.entity.audit.payload.AnnotationStyledPayload;
import com.tradingviewj.server.entity.audit.payload.ReplayActionPayload;
import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.drawing.DrawingStyle;
import com.tradingviewj.server.entity.drawing.FibLevel;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.DrawingRepository;
import com.tradingviewj.server.ws.AnnotationCommand;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.math.BigDecimal;
import java.util.List;
import java.util.Locale;

/**
 * 盘面图形（趋势线 / 射线 / 矩形 / 箭头 / 斐波那契）的读写。
 *
 * <h2>归属：图形的附体是 K 线，不是账户</h2>
 *
 * <p>表上<strong>没有 {@code account_id}</strong>（见 {@code schema.sql} 第 4 条）。
 * 一条线只要画在 {@code symbolId} 的时间轴上，就属于这块盘面：换账户做同一张图，
 * 昨天画的线还在。它不参与余额、回撤、一致性、盈利日里的任何一项统计。
 *
 * <p>但<strong>命令</strong>仍然带 {@code accountId} —— 那不是归属，是「现在谁在看这块盘面」：
 * 要靠它拿到 {@code symbolId}（账户绑定唯一标的）和 {@code lastBarTime}
 * （哪根之前的数据已经揭示，锚点不许越过它）。这两个都是账户才知道的东西。
 *
 * <h2>每条动作都写进操作记录</h2>
 *
 * <p>画线不改余额，但它<strong>改盘面</strong>——复盘时要能重放出「我在这一根上画了这条线」。
 * 所以五条动作都走 {@link AuditLogService}，各占一个 {@code seq}
 * （和 Go 版把画线当作「一步」是同一个口径）。
 *
 * <h2>锚点不许落在未来</h2>
 *
 * <p>图形的横坐标是「某根 K 线的时间 + 向右偏移」。如果允许锚定一根还没揭示的 K 线，
 * 前端 {@code xOf()} 找不到它就会整条不画 —— 更要紧的是，那样等于把后面的行情
 * 提前泄露给了使用者。所以 {@link #anchor} 拿账户的 {@code lastBarTime} 当上界。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class AnnotationService {

    private final DrawingRepository drawings;
    private final PropFirmAccountService accounts;
    private final AuditLogService audit;

    /**
     * 某个标的上的全部图形，按创建时间升序。
     *
     * <p>给快照用（连上之后要一次装完整块盘面）。只有 {@code symbolId} 一个条件 ——
     * 账户在这里不参与筛选，理由见类注释。
     */
    @Transactional(readOnly = true)
    public List<AnnotationView> list(String symbolId) {
        return drawings.findBySymbolId(symbolId).stream().map(AnnotationView::of).toList();
    }

    /**
     * 画一个图形。
     *
     * @throws BusinessException 账户不存在 / kind 不认识 / 锚点缺失或落在未来 / id 重号
     */
    @Transactional
    public TradingService.Event add(String accountId, AnnotationCommand cmd) {
        if (cmd == null) {
            throw new BusinessException("缺少 annotation");
        }
        PropFirmAccount account = accounts.requireExisting(accountId);
        Drawing.Kind kind = AnnotationStyleRules.kindOf(cmd.kind());
        Anchor start = anchor(account, cmd.start(), "起点");
        Anchor end = anchor(account, cmd.end(), "终点");

        String id = cmd.id() == null ? "" : cmd.id().trim();
        if (!id.isEmpty() && drawings.existsById(id)) {
            throw new BusinessException("图形 id 重复：" + id + "（前端生成的 id 撞号了）");
        }
        Drawing drawing = id.isEmpty()
                ? Drawing.of(account.getSymbolId(), kind, start, end, actor())
                : Drawing.of(id, account.getSymbolId(), kind, start, end, actor());
        applyStyle(drawing, kind, cmd.style());
        drawing.setSnapped(Boolean.TRUE.equals(cmd.snapped()));

        Drawing saved = drawings.save(drawing);
        log.debug("账户 {} 在 {} 上画了 {}（{}）", accountId, account.getSymbolId(), kind, saved.getId());
        return record(account, new AnnotationAddedPayload(AnnotationView.of(saved)));
    }

    /**
     * 拖动端点。<strong>两端都要给</strong> —— 只改一端会让射线 / 矩形的另一端
     * 停在用户没预期的地方。
     *
     * @throws BusinessException 这个标的上没有这个图形 / 锚点落在未来
     */
    @Transactional
    public TradingService.Event move(String accountId, AnnotationCommand cmd) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        Drawing drawing = require(account, cmd);
        drawing.moveTo(anchor(account, cmd.start(), "起点"), anchor(account, cmd.end(), "终点"));
        drawing.setSnapped(Boolean.TRUE.equals(cmd.snapped()));
        log.debug("账户 {} 挪动了图形 {}", accountId, drawing.getId());
        return record(account, new AnnotationMovedPayload(AnnotationView.of(drawing)));
    }

    /**
     * 换样式（用户点了另一条模板）。<strong>不动端点</strong> —— 这是和 {@link #move}
     * 分开两条动作的全部意义。
     *
     * <p>{@code kind} 不跟着改：图形是什么形状在画下去那一刻就定了，
     * 换模板只是换颜色线宽。传了别的 kind 就忽略。
     *
     * @throws BusinessException 这个标的上没有这个图形
     */
    @Transactional
    public TradingService.Event style(String accountId, AnnotationCommand cmd) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        Drawing drawing = require(account, cmd);
        applyStyle(drawing, drawing.getKind(), cmd.style());
        log.debug("账户 {} 改了图形 {} 的样式", accountId, drawing.getId());
        return record(account, new AnnotationStyledPayload(AnnotationView.of(drawing)));
    }

    /**
     * 删掉一个图形。只有用户自己点删除才会消失 —— 删账户、清交易数据都不碰它。
     *
     * @throws BusinessException 这个标的上没有这个图形
     */
    @Transactional
    public TradingService.Event delete(String accountId, String annotationId) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        Drawing drawing = drawings.findById(annotationId == null ? "" : annotationId.trim())
                .filter(d -> d.getSymbolId().equals(account.getSymbolId()))
                .orElseThrow(() -> new BusinessException("这块盘面上没有图形 " + annotationId));
        drawings.delete(drawing);
        log.debug("账户 {} 删除了图形 {}", accountId, drawing.getId());
        return record(account, new AnnotationDeletedPayload(drawing.getId()));
    }

    /**
     * 一次清掉这块盘面的全部图形。
     *
     * <p>发一条广播、写一条流水，<strong>不连发 N 条删除</strong>：
     * 那条路会在操作记录里留下 N 行噪音，而且中途断线会留下「删了一半」的状态。
     * 已经是空的就返回 {@code count=0}，照常记一条（用户确实点了那个按钮）。
     */
    @Transactional
    public TradingService.Event clear(String accountId) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        List<Drawing> all = drawings.findBySymbolId(account.getSymbolId());
        List<String> ids = all.stream().map(Drawing::getId).toList();
        drawings.deleteAll(all);
        log.debug("账户 {} 清空了 {} 上的 {} 个图形", accountId, account.getSymbolId(), ids.size());
        return record(account, new AnnotationClearedPayload(ids.size(), ids));
    }

    // ---------------- 内部 ----------------

    /** 写一条操作记录并包成广播。{@code barTime} 用账户「现在停在哪根」—— 那才是画线的时刻。 */
    private TradingService.Event record(PropFirmAccount account, ReplayActionPayload payload) {
        String barTime = account.getLastBarTime();
        var event = audit.append(account.getId(), account.getSymbolId(), barTime, Actor.HUMAN, payload);
        return new TradingService.Event(account.getId(), payload.action(), payload, event.getSeq());
    }

    /**
     * 按 id 找图形，并确认它<strong>属于这块盘面</strong>。
     *
     * <p>不带上 {@code symbolId} 的话，拿到另一个标的的图形 id 就能把它挪走 ——
     * 而那是一条用户看不见、也没法解释的变化（他在 A 的图上，改的是 B 的线）。
     */
    private Drawing require(PropFirmAccount account, AnnotationCommand cmd) {
        if (cmd == null || cmd.id() == null || cmd.id().isBlank()) {
            throw new BusinessException("缺少图形 id");
        }
        return drawings.findById(cmd.id().trim())
                .filter(d -> d.getSymbolId().equals(account.getSymbolId()))
                .orElseThrow(() -> new BusinessException("这块盘面上没有图形 " + cmd.id()));
    }

    /**
     * 协议锚点 → 实体锚点，并校验「必须是已揭示的 K 线」。
     *
     * <p>时间比较用<strong>字符串</strong>：{@code yyyy-MM-dd HH:mm} 是定长明文，
     * 字典序就是时间序。这不是偷懒 —— K 线时间在库里、协议里、CSV 里都是这一串明文，
     * 解析成 {@code Instant} 再比，反而要处理「本地时间没有时区」的问题
     * （同一个 {@code 09:00} 在夏令时前后是两个不同的绝对时刻，而盘面上它是同一根）。
     *
     * @throws BusinessException 缺时间 / 缺价格 / 偏移为负 / 锚点在那根之后
     */
    private static Anchor anchor(PropFirmAccount account, AnnotationPointView point, String which) {
        if (point == null || point.time() == null || point.time().isBlank()) {
            throw new BusinessException("缺少" + which + "：图形必须锚定在某根 K 线上");
        }
        if (point.price() == null) {
            throw new BusinessException("缺少" + which + "价格");
        }
        if (point.offset() < 0) {
            throw new BusinessException(which + "偏移不能为负：" + point.offset());
        }
        String last = account.getLastBarTime();
        if (last != null && point.time().compareTo(last) > 0) {
            throw new BusinessException(which + " " + point.time() + " 在那根 K 线之后（现在只揭示到 "
                    + last + "）：图形不能锚定还没走到的行情");
        }
        return new Anchor(point.time(), point.price(), point.offset());
    }

    /** 把协议样式写进实体（含斐波那契四条线）。样式一律先过一遍夹取。 */
    private static void applyStyle(Drawing drawing, Drawing.Kind kind, AnnotationStyleView in) {
        AnnotationStyleView style = AnnotationStyleRules.sanitize(kind, in);
        drawing.setStyle(new DrawingStyle(
                style.color(),
                style.width(),
                "dashed".equals(style.dash()) ? DrawingStyle.Dash.DASHED : DrawingStyle.Dash.SOLID,
                style.fill(),
                style.fillOpacity()));
        drawing.setFibLevels(kind == Drawing.Kind.FIB && style.levels() != null
                ? style.levels().stream()
                        .map(level -> new FibLevel(BigDecimal.valueOf(level.ratio()), level.color()))
                        .toList()
                : List.of());
    }

    private static String actor() {
        return Actor.HUMAN.name().toLowerCase(Locale.ROOT);
    }
}
