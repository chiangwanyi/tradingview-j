package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmStageLogRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.math.BigDecimal;
import java.time.Instant;
import java.util.List;

/**
 * PF 账户的生命周期：开、删、"能不能在这儿动手"、以及阶段迁移。
 *
 * <p>这个类是<strong>三条业务规则的唯一落点</strong>：
 *
 * <ol>
 *   <li><strong>必须先有账户才能操作。</strong> 任何入口第一步都该过一道闸门，但
 *       <strong>两道闸门不混用</strong>：{@link #requireExisting} 只要账户存在
 *       （看盘、步进、撤单、退仓 —— 失败的账户也能做），
 *       {@link #requireTradable} 才管下单（失败的账户一律拒绝）。领域方法（各实体的工厂方法）
 *       保证「不给账户就造不出对象」，这两个方法保证「给的那个账户真的能这么用」。</li>
 *   <li><strong>删账户只删「账户本身」，盘面数据不删。</strong> {@link #delete} 删的是
 *       {@code prop_firm_account} 这一行 + 属于它的 {@code prop_firm_stage_log} 流水。
 *       图形、挂单、成交、语音、操作记录、机器人、交易记录、指标<strong>全部保留</strong>，
 *       它们的 {@code account_id} 原样留着（指向一个已经不存在的账户），既不置空也不级联删除。</li>
 *   <li><strong>阶段迁移要留痕。</strong> 每次 {@code EVAL → FUNDED → PAYOUT} 都写一行
 *       {@link PropFirmStageLog}，所以「账户是考出来的还是直接开的」事后可以查。</li>
 * </ol>
 *
 * <p>为什么删除要过一层 service 而不是直接调 {@code repository.deleteById}：
 * 「什么跟着账户走、什么留下来」是这个产品的明确规则，不是数据库的默认行为。
 * 分界线是<strong>「这行数据描述的是账户本身，还是盘面上发生的事」</strong>：
 * 阶段流水描述的是账户自己的时间轴（账户没了，它就没有归属，也再没有任何入口能读到），
 * 跟着删；成交/挂单/交易记录是盘面上发生过的事实，删掉就等于篡改复盘，必须留。
 * 把这条规则写在一个有名字的方法上，下一个想加「删账户顺便删策略」的人会在改这里时先看到这段注释。
 */
@Service
@RequiredArgsConstructor
public class PropFirmAccountService {

    private final PropFirmAccountRepository accounts;
    private final PropFirmTemplateRepository templates;
    private final PropFirmStageLogRepository stageLogs;
    private final PropFirmStartPicker startPicker;
    private final PropFirmJudge judge;

    /**
     * 按模板开一个账户，并<strong>当场抽好起点</strong>。
     *
     * <p>阶段由模板决定：拿 FUNDED 模板建出来的账户<strong>直接从 FUNDED 开始</strong>
     * （「有的是创建的时候就是 FUNDED」就是这条路径），阶段流水里只有一行 {@code CREATED}。
     *
     * <p><strong>标的在这一刻定下，此后不可改</strong>（2026-10-30）。起点也只在
     * <strong>这个标的</strong>的日文件里抽 —— 否则起点那根 K 线会在账户自己的图上不存在，
     * 日历、复核日、回放起点全都建在一个不存在的坐标上。
     *
     * <p>起点由 {@link PropFirmStartPicker} 从该标的的日文件里随机抽一份、定位到当天
     * 09:00。抽不到就<strong>不建账户</strong>：一个没有起点的考核，日历、交易日、回放
     * 全都无从算起，建出来只会变成一个到处要判空的对象。所以先在事务里把起点拿到，
     * 再落库 —— 抽失败时库里一条残留都没有。
     *
     * @param templateId 用哪套考核规则（规则会在这一刻复制到账户上）
     * @param symbolId   这个账户唯一可交易的标的；<strong>必填</strong>，之后没有任何入口能改
     * @param name       账户名，用户自己起
     * @throws BusinessException 模板不存在 / 缺规则、标的不存在，或者该标的目录下没有可用的 K 线
     */
    @Transactional
    public PropFirmAccount open(String templateId, String symbolId, String name) {
        PropFirmTemplate template = templates.findById(templateId)
                .orElseThrow(() -> new BusinessException("模板不存在: " + templateId));
        if (template.getRules() == null || template.getRules().getAccountSize() == null) {
            throw new BusinessException("模板缺少账户规模，开不出账户: " + templateId);
        }
        // 先抽起点再落库：抽不到就整个操作失败，不留下半成品账户。
        // 标的存不存在也在这一步判（抽不到就不可能有起点）。
        PropFirmStartPicker.Pick pick = startPicker.pick(symbolId);
        PropFirmAccount account = accounts.save(PropFirmAccount.open(template, symbolId, name,
                pick.barTime(), pick.dayFile()));
        stageLogs.save(PropFirmStageLog.begin(account.getId(), 0, account.getStage(),
                account.getBaseBalance(), account.getStageStartedAt(), PropFirmStageLog.Reason.CREATED));
        return account;
    }

    /**
     * 拿一个<strong>存在</strong>的账户 —— <strong>不看状态</strong>：失败（{@code FAILED}）的账户也放行。
     *
     * <p>这是「看盘与收摊」这一类动作的入口：加载盘面（快照）、【下一根】、浮盈浮亏、
     * 以及撤单 / 改价 / 一键退仓。理由是<strong>它们都不会让一个已经考挂的账户变得更糟</strong>：
     * 失败那一刻持仓已经被市价平掉、挂单已经撤光（见 {@code TradingService#closeOut}），
     * 之后这些动作都是空转或者收尾。而人还需要回到那个账户上看复盘、标注、改名 ——
     * 把整页锁死等于把「考挂那一场」的录像也一起锁了，那是另一笔损失。
     *
     * <p>⚠️ <strong>下单不能走这里</strong>，必须走 {@link #requireTradable}：
     * 「能看」和「能下单」是本系统里唯一需要分开的两件事。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireExisting(String accountId) {
        return accounts.findById(accountId)
                .orElseThrow(() -> new BusinessException("PF 账户不存在，请先创建账户: " + accountId));
    }

    /**
     * 拿一个<strong>还在考核中</strong>的账户（{@code ACTIVE}），拿不到就抛业务异常。
     *
     * <p>用于那些「这件事发生了就再也改不掉」的入口：判失败、升级、推盘面时间
     * （改的是账户自己的状态位，不是盘面上的事实）。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireActive(String accountId) {
        PropFirmAccount account = requireExisting(accountId);
        if (!account.isOpen()) {
            throw new BusinessException("PF 账户当前是 " + account.getStatus() + "，不能再产生新的操作");
        }
        return account;
    }

    /**
     * 拿一个<strong>可以下单</strong>的账户 —— 挂单、市价单的唯一闸门。
     *
     * <p>四条都要过：
     *
     * <ul>
     *   <li><strong>已判失败</strong>（撞了亏损线）。失败是终态：账户仍可打开、看盘、标注，
     *       但<strong>不再接受任何新单</strong> —— 这正是 {@link #requireExisting} 与这里的分界。</li>
     *   <li>{@link PropFirmStage#PAYOUT} 阶段虽然"开着"（还在处理出金），但它<strong>是终点</strong>，
     *       不再接受下单。画图、录音这类"看盘动作"仍然允许 —— 用户可能想在收工之后再标注一遍。</li>
     *   <li><strong>盘面停在禁止交易时段</strong>（{@link #requireOpenSession}）。这是 2026-11-01 新增的：
     *       {@code [04:00, 06:00]} 两端都关，最早可交易 06:05、最晚 03:55。</li>
     *   <li><strong>已达标但还没升级</strong>的账户（{@link PropFirmJudge#requireNotFrozen}）。
     *       达标那一刻持仓已经被平、账户被冻结，但仍处于 ACTIVE（还要回来看复盘、改名、录像），
     *       所以这件事只能现算，不能靠 {@code status} 表达。</li>
     * </ul>
     *
     * <p>⚠️ <strong>禁交易时段的拦截只加在下单路径上</strong>（挂单 / 市价）。
     * 撤单、改价、一键退仓走 {@link #requireExisting}，在这个时段里照常可用 ——
     * 它们只会<strong>减少</strong>风险，锁掉它们等于让人对着一个平不掉的仓位干瞪眼。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireTradable(String accountId) {
        PropFirmAccount account = requireExisting(accountId);
        if (!account.isOpen()) {
            throw new BusinessException("账户「" + account.getName() + "」已判失败（撞了亏损线）"
                    + "，只能看盘、标注和收尾，不能再下单 —— 要重新考一次请新建一个账户");
        }
        if (!account.isTradable()) {
            throw new BusinessException("PF 账户已进入 " + account.getStage() + " 阶段，不再接受下单");
        }
        requireOpenSession(account);
        judge.requireNotFrozen(account);
        return account;
    }

    /**
     * 盘面现在这一根是不是落在禁交易时段里，落在里面就拒绝下单。
     *
     * <p>判据是账户的 {@code lastBarTime}（用户看到的「现在」），不是服务器真实时钟 ——
     * 这是复盘盘，时间由【下一根】推着走，跟北京时间没有关系。
     *
     * <p>账户还没有盘面（{@code lastBarTime} 为空）时放行：那种情况下连市价单都取不到收盘价，
     * 后面的行情检查会给出更准确的报错，在这里拦一句「禁止交易时段」只会误导。
     */
    private static void requireOpenSession(PropFirmAccount account) {
        String at = account.getLastBarTime();
        if (at == null || at.length() < 16) {
            return;
        }
        boolean tradable;
        try {
            tradable = PropFirmClock.tradable(at);
        } catch (RuntimeException e) {
            // 时间格式坏了不是「能不能交易」的问题，交给后面的行情逻辑报错
            return;
        }
        if (tradable) {
            return;
        }
        throw new BusinessException("盘面现在停在 " + at + "，落在禁止交易时段 04:00–06:00 内："
                + "这段时间只能平仓和撤单，不能挂单、不能市价开仓。"
                + "按【下一根】走到 06:05 之后再下单（每天最早 06:05、最晚 03:55）");
    }

    /**
     * 删除账户 —— <strong>删这一行 + 它的阶段流水</strong>。
     *
     * <p>阶段流水（{@code prop_firm_stage_log}）<strong>跟着账户一起删</strong>：
     * 它记的是「这个账户自己当过哪几个阶段」，账户行一没，它就既没有归属、
     * 也没有任何入口能读到（{@code GET /{id}/stages} 要的就是这个 id）。
     *
     * <p>除此之外的东西<strong>原地保留</strong>：成交、交易记录、语音、操作记录、图形、
     * 指标、机器人，仍然记着原来那个 {@code account_id}。想删那些数据，只能由用户一条一条地
     * 显式删 —— 它们记录的是盘面上发生过什么，删掉就是篡改复盘。
     *
     * <p><strong>不可逆</strong>：流水删了就没有审计痕迹，事后无法再回答「这个账户是考出来的
     * 还是直接开的」。要留档就先导出（{@code GET /api/pf-accounts/{id}/stages}）再删。
     *
     * <p>幂等：账户已经不在了也不报错；流水仍然试着清一次，这样老版本删过的账户
     * （当时留下了孤立流水）再删一次同一个 id 时能顺手收拾干净。
     */
    @Transactional
    public void delete(String accountId) {
        boolean exists = accounts.existsById(accountId);
        // 顺序：先流水后账户。反过来的话中途失败会留下「账户没了、流水还在」的孤儿行，
        // 而那个 id 已经没有任何入口能再触发一次清理。
        stageLogs.deleteByAccountId(accountId);
        if (exists) {
            accounts.deleteById(accountId);
        }
    }

    /**
     * 升级到下一阶段。由 {@code PropFirmSession} 判出 {@code PROMOTED} 之后调用。
     *
     * <p>做四件事，顺序不能换：
     *
     * <ol>
     *   <li>把当前阶段那段流水结掉（写入结束余额、单日最大盈利、盈利日数）；</li>
     *   <li>账户换规则、换余额基准（<strong>余额重置回账户规模</strong>，EVAL 的盈利不带进 FUNDED）；</li>
     *   <li>开一段新的流水（{@code reason=PROMOTED}）；</li>
     *   <li>{@code FUNDED → PAYOUT} 时不开新流水之外的额外动作：{@code status} 保持 ACTIVE，
     *       「不再交易」由 {@code stage} 表达（{@code COMPLETED} 这个状态已废弃）。</li>
     * </ol>
     *
     * @param session 判出升级的那个会话，用来把统计量写进流水；可以传 {@code null}
     *                （手工升级时没有会话可写，流水里那几个统计列留空）
     * @throws BusinessException 账户不在考核中、或模板没配下一阶段
     */
    @Transactional
    public PropFirmAccount promote(String accountId, PropFirmSession session) {
        PropFirmAccount account = requireActive(accountId);
        if (account.getStage() == PropFirmStage.PAYOUT) {
            // PAYOUT 是链的尽头。没有这一条的话，「else → PAYOUT」会把已经在 PAYOUT 的账户
            // 再"升"一次 PAYOUT：余额被重置回账户规模，EVAL/FUNDED 的账就这么被擦掉了。
            throw new BusinessException("PF 账户已在 PAYOUT 阶段，没有下一阶段可升: " + accountId);
        }
        PropFirmStage next = account.getStage() == PropFirmStage.EVAL
                ? PropFirmStage.FUNDED
                : PropFirmStage.PAYOUT;

        PropFirmTemplate nextTemplate = null;
        if (next == PropFirmStage.FUNDED) {
            PropFirmTemplate current = templates.findById(account.getTemplateId())
                    .orElseThrow(() -> new BusinessException(
                            "账户引用的模板不存在，无法确定下一阶段: " + account.getTemplateId()));
            if (current.getNextTemplateId() == null || current.getNextTemplateId().isBlank()) {
                throw new BusinessException(
                        "模板「" + current.getName() + "」没有配置 next_template_id，升不到下一阶段");
            }
            nextTemplate = templates.findById(current.getNextTemplateId())
                    .orElseThrow(() -> new BusinessException(
                            "下一阶段模板不存在: " + current.getNextTemplateId()));
        }

        Instant at = Instant.now();
        closeCurrentStage(account, next == PropFirmStage.PAYOUT
                ? PropFirmStageLog.EndReason.PAYOUT_DONE
                : PropFirmStageLog.EndReason.PROMOTED, session, at);

        account.enterStage(next, nextTemplate, at);
        accounts.save(account);

        int seq = stageLogs.findTopByAccountIdOrderBySeqDesc(accountId)
                .map(log -> log.getSeq() + 1).orElse(0);
        stageLogs.save(PropFirmStageLog.begin(accountId, seq, next, account.getBaseBalance(),
                at, PropFirmStageLog.Reason.PROMOTED));
        return account;
    }

    /**
     * 判失败：撞了亏损线。账户置 {@code FAILED}，当前阶段流水记 {@code FAILED}。
     *
     * <p><strong>本方法只改状态位，不收摊。</strong> 「平仓 + 撤单」在
     * {@code TradingService#closeOut} 里，必须先于本方法调用 —— 那一步要读盘面拿成交价，
     * 而且和「一键退仓」共用同一段实现（一条平仓路径，账才只有一份口径）。
     * 两个入口（{@code step} 撞线、{@code POST /fail} 手动判死）都由
     * {@code TradingService#failAccount} 串起来，别在这里另开一遍。
     *
     * <p>失败<strong>不再升级、也不会自动重开</strong> —— 重考要新建一个账户，
     * 这样两次考核的成交天然不会混在一个 {@code accountId} 下。
     *
     * <p>失败之后账户<strong>仍然打得开</strong>（看盘、标注、改名），下单由
     * {@link #requireTradable} 逐次拒绝。
     */
    @Transactional
    public PropFirmAccount fail(String accountId, PropFirmSession session) {
        PropFirmAccount account = requireActive(accountId);
        closeCurrentStage(account, PropFirmStageLog.EndReason.FAILED, session, Instant.now());
        account.markFailed();
        return accounts.save(account);
    }

    /**
     * 把账户的「盘面最新时间」推到某一根 K 线（交易页按【下一根】时调）。
     *
     * <p>写的是 {@link PropFirmAccount#getLastBarTime()} —— 「继续交易」打开交易页时
     * 就揭示到这一根。规则在实体上：<strong>只能往后走</strong>（见
     * {@link PropFirmAccount#advanceBarTime(String)}），这里只负责「账户得存在」。
     *
     * <p><strong>失败的账户也能推</strong>（2026-11-01）：考挂之后盘面还是一页历史，
     * 继续往后看是复盘，不是交易 —— 反正新单已经被 {@link #requireTradable} 拦死了。
     *
     * @throws BusinessException 账户不存在，或时间格式不对、比当前值更早
     */
    @Transactional
    public PropFirmAccount advanceCursor(String accountId, String barTime) {
        PropFirmAccount account = requireExisting(accountId);
        account.advanceBarTime(barTime);
        return accounts.save(account);
    }

    /**
     * 下单前的手数闸门：这一笔成交之后，该品种的净持仓会不会超过当前利润档位。
     *
     * <p>顺带把「这个账户只做它绑定的那个标的」落下：{@code symbolId} 和账户上的
     * {@link PropFirmAccount#getSymbolId()} 不一致就直接拒绝。这是下单路径上现存的
     * 唯一入口，所以守卫放这里 —— 以后真接上撮合时，它已经是必经之路。
     *
     * @param netAfterFill 成交之后该品种的净持仓（买正卖负）
     * @throws BusinessException 超限，或者这个标的不是该账户的标的
     */
    @Transactional(readOnly = true)
    public void checkPositionLimit(String accountId, String symbolId, BigDecimal netAfterFill) {
        PropFirmAccount account = requireTradable(accountId);
        requireBoundSymbol(account, symbolId);
        PropFirmPositionLimit.requireWithinLimit(account.getRules(),
                account.netProfitInStage(), symbolId, netAfterFill);
    }

    /**
     * 这个账户能不能交易这个标的，不能就抛业务异常。
     *
     * <p>公开出来是给将来的下单 / 挂单 / 成交入口复用的：<strong>每一处都得问一次</strong>，
     * 而不是只在某一个按钮上判 —— 只判一处等于没判。
     *
     * @throws BusinessException 账户不在考核中，或者标的不是它绑定的那个
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireSymbol(String accountId, String symbolId) {
        PropFirmAccount account = requireActive(accountId);
        requireBoundSymbol(account, symbolId);
        return account;
    }

    private static void requireBoundSymbol(PropFirmAccount account, String symbolId) {
        if (!account.tradesSymbol(symbolId)) {
            throw new BusinessException("账户「" + account.getName() + "」只做 "
                    + account.getSymbolId() + "，不能交易 " + symbolId
                    + "（标的在创建账户时选定，不可切换）");
        }
    }

    /** 取一个账户，不判断「能不能往上挂操作」—— 那是 {@link #requireActive} 的事。 */
    @Transactional(readOnly = true)
    public PropFirmAccount get(String accountId) {
        return accounts.findById(accountId)
                .orElseThrow(() -> new BusinessException("PF 账户不存在: " + accountId));
    }

    /**
     * 取一个账户，<strong>顺便把「已达标待升级」算出来填上</strong>（{@code account.passed}）。
     *
     * <p>只在<strong>读接口</strong>用（{@code GET /api/pf-accounts/{id}}）：那一次请求就是为了
     * 让界面决定「升级按钮出不出现」，多花一次重放（十几到几十毫秒）换掉前端自己推规则。
     *
     * <p>⚠️ <strong>不要换掉 {@link #get}</strong>：{@code requireExisting} 走的是 {@code get}，
     * 那是看盘 / 撤单 / 退仓的热路径，每按一次【下一根】就要读一次账户，
     * 在那里重放一遍整段盘面纯属浪费 —— 而写操作本来也不需要这个数。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount detail(String accountId) {
        PropFirmAccount account = get(accountId);
        fillDerived(account);
        return account;
    }

    /**
     * 把两个「读的时候才算得出来」的结论填到实体上（都不落库）：
     * {@code originStage}（建户时在哪个阶段）和 {@code passed}（已达标待升级）。
     *
     * <p>放在一处是因为它们的调用时机<b>完全一样</b> —— 都只在读接口里，
     * 都是给界面判断用的。写操作的返回里两个字段都缺（前端读到 {@code undefined}），
     * 那些地方不需要它们，也不该为此多读一次流水、多重放一次盘面。
     */
    private void fillDerived(PropFirmAccount account) {
        fillOriginStage(account);
        fillPassed(account);
    }

    /**
     * 填「建户时在哪个阶段」：阶段流水的第一行（{@code seq = 0}）写的那个阶段。
     *
     * <p>用它而不是比较 {@code stageStartedAt} 和 {@code startedAt}：那种比较依赖
     * 「两个时刻一定不同」，而真正的事实是「这个账户建户时是哪一场考核」——
     * 流水本来就是这份事实的档案，账户详情页的「行数就是身份」说的也是它。
     *
     * <p>没有流水（老数据 / 流水被清过）就留 {@code null}：那是「不知道」，
     * 别拿现在的阶段冒充建户阶段 —— 冒充出来的结论恰好会把「考的」说成「直开的」。
     */
    private void fillOriginStage(PropFirmAccount account) {
        stageLogs.findFirstByAccountIdOrderBySeqAsc(account.getId())
                .ifPresent(log -> account.markOriginStage(log.getStage()));
    }

    /**
     * 算「已达标待升级」并填到实体上（不落库）。
     *
     * <p>三类直接给 {@code false}，连重放都不做：
     *
     * <ul>
     *   <li><strong>已判失败</strong> —— 终态，没有下一阶段可升；</li>
     *   <li><strong>已在 PAYOUT</strong> —— 链的尽头。</li>
     * </ul>
     *
     * <p>重放本身失败（标的被删了、规则快照缺字段）也退化成 {@code false}：
     * 那是一个「详情页少一颗按钮」的问题，不该变成「账户列表打不开」。
     */
    private void fillPassed(PropFirmAccount account) {
        if (!account.isOpen() || account.getStage() == PropFirmStage.PAYOUT) {
            account.markPassed(false);
            return;
        }
        try {
            account.markPassed(judge.assess(account).frozen());
        } catch (RuntimeException e) {
            account.markPassed(false);
        }
    }

    /**
     * 账户列表，新建的在前。
     *
     * <p>每个元素都带上 {@code promoted}（考出来的吗）和 {@code passed}（已达标待升级），
     * 见 {@link #fillDerived} —— 首页据此决定阶段标签怎么写、【升级】按钮出不出来。
     * 代价是「每账户一次流水查询 + 一次重放」，和「每个账户都去交易页开一遍」同一份量级，
     * 只是提前在一次请求里做完。
     *
     * @param status {@code null} = 全部（含 {@code FAILED}）
     */
    @Transactional(readOnly = true)
    public List<PropFirmAccount> list(PropFirmAccount.Status status) {
        List<PropFirmAccount> all = status == null
                ? accounts.findAllByOrderByStartedAtDesc()
                : accounts.findByStatusOrderByStartedAtDesc(status);
        all.forEach(this::fillDerived);
        return all;
    }

    /**
     * 改账户名。<strong>走 {@link #get} 而不是 {@link #requireActive}</strong>：
     * 改名不是对盘面的操作，失败或走到 PAYOUT 的账户也应该能改成「2024 第一次考挂的那个」。
     *
     * @throws BusinessException 账户不存在；或缺名字（实体校验失败）
     */
    @Transactional
    public PropFirmAccount rename(String accountId, String name) {
        PropFirmAccount account = get(accountId);
        account.rename(name);
        return accounts.save(account);
    }

    /** 这个账户当过的所有阶段，按进入顺序（「考来的」和「直接开的」看行数）。 */
    @Transactional(readOnly = true)
    public List<PropFirmStageLog> stageHistory(String accountId) {
        return stageLogs.findByAccountIdOrderBySeqAsc(accountId);
    }

    private void closeCurrentStage(PropFirmAccount account, PropFirmStageLog.EndReason reason,
                                   PropFirmSession session, Instant at) {
        stageLogs.findFirstByAccountIdAndEndedAtIsNullOrderBySeqDesc(account.getId())
                .ifPresent(log -> {
                    BigDecimal balance = session == null ? null : session.balance();
                    BigDecimal maxDay = session == null ? null : session.maxDayNet();
                    Integer days = session == null ? null : session.profitDays();
                    log.close(reason, balance == null ? account.getBalance() : balance,
                            maxDay, days, at);
                    stageLogs.save(log);
                });
    }
}
