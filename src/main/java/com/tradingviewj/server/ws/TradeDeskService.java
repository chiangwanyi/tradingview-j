package com.tradingviewj.server.ws;

import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.BarAdvancedPayload;
import com.tradingviewj.server.entity.audit.payload.VoiceState;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.service.AuditLogService;
import com.tradingviewj.server.service.BarCursorService;
import com.tradingviewj.server.service.BarQueryService;
import com.tradingviewj.server.service.PropFirmAccountService;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolCatalog;
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
 * <h2>它和 {@code TradeSocketHandler} 的分工</h2>
 *
 * <p>Handler 只管<strong>连接</strong>（谁连着、广播给谁、怎么发）和<strong>解析</strong>
 * （这段 JSON 是哪种消息）。<strong>业务全在这里</strong>：读账户、读 CSV、推游标、写流水。
 * 分开的实际好处是这一层能脱离 WebSocket 测 —— 一个连不上的浏览器不会让你没法验证「步进一根」。
 *
 * <h2>为什么每个动作都要「先读账户再动手」</h2>
 *
 * <p>{@link PropFirmAccountService#requireActive} 是三条业务规则的唯一落点（账户必须存在、
 * 必须还在考核中）。绕过它直接读库，一个已判失败的账户就会继续往后放 K 线、继续写流水 ——
 * 而那些流水事后会被算进它的交易日和盈利日。
 *
 * <h2>目前接了哪些动作</h2>
 *
 * <p>只有两个：{@link #snapshot}（连上时的盘面）和 {@link #step}（【下一根】）。
 * 下单 / 撤单 / 平仓 / 画线还没接 —— 消息类型在 {@code TradeSocketHandler} 里<strong>显式拒绝</strong>，
 * 不静默丢掉：静默丢掉会让「协议写错了一个字段」表现成「点了没反应」。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class TradeDeskService {

    /** 语音每根的上限。和 {@code AudioClip} 的规则同一个数，前端只照它显示，不自己数。 */
    private static final int VOICE_MAX_PER_BAR = 9;

    private final PropFirmAccountService accounts;
    private final SymbolCatalog catalog;
    private final CsvBarStore bars;
    private final BarQueryService barQuery;
    private final BarCursorService cursor;
    private final AuditLogService audit;

    /**
     * 一次步进的结果，交给 Handler 包成广播。
     *
     * @param payload 就是落进 {@code audit_event.payload_json} 的那份东西 ——
     *                广播和流水<strong>必须是同一份</strong>，否则「回放看到的过程」和
     *                「当时看到的过程」会慢慢分叉
     * @param seq     这条流水的序号
     */
    public record StepResult(String accountId, String symbolId, BarAdvancedPayload payload, int seq) {
    }

    /**
     * 账户现在的盘面。
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
        SymbolSpec spec = symbol(account.getSymbolId());
        String stoppedAt = account.getLastBarTime();

        BarSeriesResponse series = window(spec, stoppedAt);
        String barTime = series.bars().isEmpty()
                ? (stoppedAt == null ? "" : stoppedAt)
                : series.bars().get(series.bars().size() - 1).time();

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
                series.bars());
    }

    /**
     * 【下一根】：把盘面往后放一根。
     *
     * <p>顺序是<strong>固定的</strong>，四步都不能换：
     *
     * <ol>
     *   <li>找出当前这根之后的第一根（{@link BarCursorService}，会跨日文件、会跳过空文件）；</li>
     *   <li>把账户的 {@code lastBarTime} 推过去 —— <strong>先推账户再写流水</strong>：
     *       两者的值来自同一个 {@code barTime}，但账户是「下次打开停在哪」的唯一来源，
     *       流水是「当时发生过什么」的日志。先写流水再推账户，中间失败就留下一条
     *       「记录了步进、但账户没动」的错位；反过来失败则只是少一条流水，盘面本身还是对的。</li>
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
    public StepResult step(String accountId) {
        PropFirmAccount account = accounts.requireActive(accountId);
        SymbolSpec spec = symbol(account.getSymbolId());
        String stoppedAt = account.getLastBarTime();

        BarCursorService.NextBar next = cursor.next(spec.folder(), stoppedAt);
        if (next == null) {
            throw new BusinessException(stoppedAt == null || stoppedAt.isBlank()
                    ? "标的 " + spec.code() + " 还没有任何 K 线文件，放不了下一根"
                    : stoppedAt + " 已经是磁盘上最后一根 K 线了：等抓取程序再录一段才能继续");
        }

        String barTime = next.bar().barTime();
        accounts.advanceCursor(accountId, barTime);

        BarAdvancedPayload payload = new BarAdvancedPayload(
                new BarView(barTime, next.bar().open(), next.bar().high(),
                        next.bar().low(), next.bar().close(), next.bar().volume()),
                next.indexInDay(),
                next.restOfDay(),
                next.hasNext(),
                // 盘面全量：这里没有挂单 / 成交 / 持仓 —— 撮合还没接通，账上本来就什么都没有。
                // 发空列表而不是省略字段，前端拿到的一定是「确实是空的」，不是「没给我」。
                List.of(),
                List.of(),
                List.of(),
                Position.FLAT,
                BigDecimal.ZERO,
                BigDecimal.ZERO,
                // fills 只装这一根触发的成交，这一根没有成交
                List.of(),
                new VoiceState(barTime, List.of(), 0, VOICE_MAX_PER_BAR));

        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.debug("账户 {} 步进到 {}（seq={}，今天还剩 {} 根）",
                accountId, barTime, event.getSeq(), next.restOfDay());
        return new StepResult(accountId, spec.code(), payload, event.getSeq());
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

    /** 账户绑定的标的。账户上的 {@code symbolId} 是标的<strong>代码</strong>，磁盘上是目录名。 */
    private SymbolSpec symbol(String code) {
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
}
