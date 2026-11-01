package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.PfView;
import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.trade.Trade;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.repository.TradeRepository;
import com.tradingviewj.server.storage.Bar;
import com.tradingviewj.server.storage.CsvBarStore;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

import java.math.BigDecimal;
import java.nio.file.Path;
import java.time.Instant;
import java.time.LocalDate;
import java.util.List;
import java.util.Map;

/**
 * 把 {@link PropFirmSession} 接到库里、盘面上：<strong>喂数据、拿读数</strong>。
 *
 * <h2>它解决的是「两套时钟」怎么落地</h2>
 *
 * <p>{@link PropFirmSession} 是个纯推进器：给它一串按时间排好的 K 线和一份成交流水，它回答
 * 「推到这一根时余额多少、亏损线在哪、有没有撞线 / 达标」。但它自己不知道数据在哪儿 ——
 * 于是这个类负责两件事，别的什么都不做：
 *
 * <ol>
 *   <li><strong>凑输入</strong>：本阶段的成交（{@link #stageTrades}）+ 从阶段起点到
 *       {@code lastBarTime} 的 K 线（{@link #feed}）；</li>
 *   <li><strong>交读数</strong>：{@link #assess} 给判定用，{@link #settle} 顺手把余额和峰值
 *       写回账户那两列缓存。</li>
 * </ol>
 *
 * <h2>为什么每次都从头重放，而不是把 session 缓存在内存里</h2>
 *
 * <p>缓存要回答「进程重启了怎么办、两个标签页同时点怎么办、账户被别处改了怎么办」，
 * 每一个答错都会变成「余额差一笔」。重放的代价是十几到几十毫秒（一天 276 根、一天一个文件），
 * 而【下一根】是手点的 —— 用这点时间换「永远和成交对得上」很划算。
 *
 * <p>⚠️ 因此本类的方法<strong>不能在同一个请求里反复调</strong>：一次 {@code market} 现在
 * 会重放两三次（闸门一次、收盘同步一次）。要优化先量，别猜。
 *
 * <h2>「本阶段的成交」怎么认</h2>
 *
 * <p>用 {@code trade.createdAt >= account.stageStartedAt}（两个都是真实时钟）。
 * 为什么不在成交流水上记阶段号：那要改表，而阶段边界在成交落库那一刻就已经确定了 ——
 * 升级发生在盘面某一步之后，之后落的成交自然属于新阶段。反过来说，
 * 这条判据要求时钟可信，所以它只用来<strong>切阶段</strong>，不参与任何金额计算。
 *
 * <h2>K 线从哪一根开始喂</h2>
 *
 * <p>从<strong>本阶段第一笔成交所在那一天的第一根</strong>开始，而不是账户的起点：
 * 中间那些没有成交的日子，日终余额恒等于起点余额、盈利日也数不上，
 * 喂不喂进去结果一样，但少读一堆日文件。
 */
@Service
@RequiredArgsConstructor
public class PropFirmJudge {

    private final CsvBarStore bars;
    private final SymbolRepository symbols;
    private final TradeRepository trades;

    /** 推进到 {@code account.lastBarTime} 为止的判定结果。 */
    public record State(PropFirmSession session, PropFirmSession.Step step) {

        /** 撞了亏损线。 */
        public boolean failed() {
            return step != null && step.event() == PropFirmSession.Event.FAILED;
        }

        /**
         * 已达成本阶段的升级条件，等用户手动升级。
         *
         * <p>{@link PropFirmSession} 只在<strong>日终</strong>判达标（真实考核就是这样），
         * 所以它和「撞线」一样，只在跨交易日的那一步才会变。
         */
        public boolean frozen() {
            return step != null && step.event() == PropFirmSession.Event.PROMOTED;
        }
    }

    /**
     * 把账户从阶段起点重放到 {@code lastBarTime}，返回判定结果（<strong>不写库</strong>）。
     *
     * <p>没有盘面（账户还没停在任何一根上、或者磁盘上没有那些日文件）时
     * {@code step} 为 {@code null}，读数退化成本阶段的起步状态 —— 不抛异常：
     * 打开交易页这件事不该因为行情被清理了就打不开。
     */
    public State assess(PropFirmAccount account) {
        if (account.getRules() == null) {
            throw new IllegalStateException("账户 " + account.getId() + " 没有规则快照，算不出考核读数");
        }
        Symbol symbol = symbols.findById(account.getSymbolId())
                .orElseThrow(() -> new BusinessException(
                        "账户的标的 " + account.getSymbolId() + " 不在 symbol 表里：算不出考核读数"));
        List<Trade> stageTrades = stageTrades(account);
        PropFirmSession session = new PropFirmSession(account.getStage(), account.getRules(),
                account.getBaseBalance(), Map.of(symbol.getId(), symbol), stageTrades);
        return new State(session, feed(session, symbol, stageTrades, account.getLastBarTime()));
    }

    /**
     * 判完顺手把余额 / 日终峰值写回账户那两列缓存，并给出一份给前端看的读数。
     *
     * <p>写的是账户实体的字段（{@code account.syncComputed}），所以<strong>调用方必须是事务性的</strong>、
     * 且这个 {@code account} 是托管对象 —— 本类不自己开事务：一个动作里的「算账」和
     * 「记成交」必须在同一个事务里，分开就会出现「成交落了库、余额没跟上」。
     */
    public PfView settle(PropFirmAccount account, State state) {
        account.syncComputed(state.session().balance(), state.session().eodPeak());
        return view(account, state);
    }

    /** 重放一遍再判，只为了拿读数（调用方自己开事务，且账户是托管的）。 */
    public PfView settle(PropFirmAccount account) {
        return settle(account, assess(account));
    }

    /** 只读地转成协议视图 —— 不写库（快照用它：打开交易页不该顺手改账户）。 */
    public PfView view(PropFirmAccount account, State state) {
        PropFirmSession session = state.session();
        BigDecimal equity = state.step() == null ? session.balance() : state.step().equity();
        return new PfView(
                account.getStatus() == null ? null : account.getStatus().name(),
                session.balance(),
                session.eodPeak(),
                session.floorLine(),
                equity,
                state.frozen(),
                state.step() == null ? null : state.step().message());
    }

    /**
     * 这个账户现在能不能下单。
     *
     * <p>「已达标待升级」和「撞线失败」是两种不同的终局：失败由 {@code status} 拦，
     * 这里拦的是前者 —— 达标那一刻持仓已经被平掉、账户被冻结，
     * 但状态还是 ACTIVE（用户还要回来看复盘、改名、录像），所以不能用 status 表达。
     *
     * @throws BusinessException 已达标、等升级
     */
    public void requireNotFrozen(PropFirmAccount account) {
        if (!assess(account).frozen()) {
            return;
        }
        throw new BusinessException("账户「" + account.getName() + "」已满足 "
                + account.getStage() + " 的升级条件，持仓已平仓、暂停接受新单："
                + "请到账户详情页点「升级」进入下一阶段");
    }

    // ---------------------------------------------------------------- 内部

    /** 本阶段的成交（按 barTime + seq 升序，和别处读成交流水用同一个顺序）。 */
    private List<Trade> stageTrades(PropFirmAccount account) {
        Instant since = account.getStageStartedAt();
        return trades.findByAccountIdOrderByBarTimeAscSeqAsc(account.getId()).stream()
                .filter(t -> since == null || t.getCreatedAt() == null || !t.getCreatedAt().isBefore(since))
                .toList();
    }

    /**
     * 按时间升序把 K 线喂给会话，返回最后一根的读数。
     *
     * <p>日文件按文件名（= 日期）升序读，所以「跨交易日」只会在该发生的时候发生；
     * 缺失的日子（周末、没抓）直接没有文件，不影响 —— 日终结算只认「新的一天的第一根」，
     * 隔了三天和隔了一天是同一件事。
     */
    private PropFirmSession.Step feed(PropFirmSession session, Symbol symbol,
                                      List<Trade> stageTrades, String untilBarTime) {
        if (untilBarTime == null || untilBarTime.length() < 10) {
            return null;
        }
        String from = stageTrades.isEmpty() ? untilBarTime : stageTrades.get(0).getBarTime();
        LocalDate fromDay = dayOfBarTime(from);
        LocalDate toDay = dayOfBarTime(untilBarTime);
        if (fromDay == null || toDay == null || toDay.isBefore(fromDay)) {
            return null;
        }
        PropFirmSession.Step last = null;
        for (Path file : bars.dayFiles(symbol.getFolder())) {
            LocalDate day = dayOfFile(file);
            if (day == null || day.isBefore(fromDay) || day.isAfter(toDay)) {
                continue;
            }
            for (Bar bar : bars.readDay(symbol.getFolder(), day)) {
                if (bar.barTime().compareTo(untilBarTime) > 0) {
                    break;
                }
                last = session.onBar(symbol.getId(), bar);
            }
        }
        return last;
    }

    /** {@code "2026-10-29 03:10"} → 03:10 所在的<strong>日文件</strong>日期（就是字符串前 10 位）。 */
    private static LocalDate dayOfBarTime(String barTime) {
        try {
            return LocalDate.parse(barTime.substring(0, 10));
        } catch (RuntimeException e) {
            return null;
        }
    }

    /** {@code 2026-10-29.csv} → 2026-10-29；名字不像日期就跳过这个文件。 */
    private static LocalDate dayOfFile(Path file) {
        String name = file.getFileName().toString();
        if (!name.endsWith(".csv") || name.length() != 14) {
            return null;
        }
        return dayOfBarTime(name);
    }
}
