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
 *   <li><strong>必须先有账户才能操作。</strong> 任何下单、画图、录音、启动机器人的入口，
 *       第一步都该调 {@link #requireActive}。领域方法（各实体的工厂方法）保证「不给账户就
 *       造不出对象」，这个方法保证「给的那个账户真的存在、真的还在考核中」。</li>
 *   <li><strong>删账户不删数据。</strong> {@link #delete} 只删 {@code prop_firm_account}
 *       这一行。图形、挂单、成交、语音、操作记录、机器人、交易记录、指标<strong>全部保留</strong>，
 *       它们的 {@code account_id} 原样留着（指向一个已经不存在的账户），既不置空也不级联删除。</li>
 *   <li><strong>阶段迁移要留痕。</strong> 每次 {@code EVAL → FUNDED → PAYOUT} 都写一行
 *       {@link PropFirmStageLog}，所以「账户是考出来的还是直接开的」事后可以查。</li>
 * </ol>
 *
 * <p>为什么删除要过一层 service 而不是直接调 {@code repository.deleteById}：
 * 「不级联」是这个产品的明确规则，不是数据库的默认行为。把这条规则写在一个有名字的方法上，
 * 下一个想加「删账户顺便删策略」的人会在改这里时先看到这段注释。
 */
@Service
@RequiredArgsConstructor
public class PropFirmAccountService {

    private final PropFirmAccountRepository accounts;
    private final PropFirmTemplateRepository templates;
    private final PropFirmStageLogRepository stageLogs;
    private final PropFirmStartPicker startPicker;

    /**
     * 按模板开一个账户，并<strong>当场抽好起点</strong>。
     *
     * <p>阶段由模板决定：拿 FUNDED 模板建出来的账户<strong>直接从 FUNDED 开始</strong>
     * （「有的是创建的时候就是 FUNDED」就是这条路径），阶段流水里只有一行 {@code CREATED}。
     *
     * <p>起点由 {@link PropFirmStartPicker} 从全部标的的日文件里随机抽一份、定位到当天
     * 09:00。抽不到就<strong>不建账户</strong>：一个没有起点的考核，日历、交易日、回放
     * 全都无从算起，建出来只会变成一个到处要判空的对象。所以先在事务里把起点拿到，
     * 再落库 —— 抽失败时库里一条残留都没有。
     *
     * @param templateId 用哪套考核规则（规则会在这一刻复制到账户上）
     * @param name       账户名，用户自己起
     * @throws BusinessException 模板不存在 / 缺规则，或者 data/ 下没有可用的 K 线
     */
    @Transactional
    public PropFirmAccount open(String templateId, String name) {
        PropFirmTemplate template = templates.findById(templateId)
                .orElseThrow(() -> new BusinessException("模板不存在: " + templateId));
        if (template.getRules() == null || template.getRules().getAccountSize() == null) {
            throw new BusinessException("模板缺少账户规模，开不出账户: " + templateId);
        }
        // 先抽起点再落库：抽不到就整个操作失败，不留下半成品账户
        PropFirmStartPicker.Pick pick = startPicker.pick();
        PropFirmAccount account = accounts.save(PropFirmAccount.open(template, name,
                pick.barTime(), pick.dayFile()));
        stageLogs.save(PropFirmStageLog.begin(account.getId(), 0, account.getStage(),
                account.getBaseBalance(), account.getStageStartedAt(), PropFirmStageLog.Reason.CREATED));
        return account;
    }

    /**
     * 拿一个<strong>可以往上挂操作</strong>的账户，拿不到就抛业务异常。
     *
     * <p>所有操作类入口（下单、画图、录音、启动机器人、开交易记录）都该先过这一关：
     * 「账户不存在」和「账户已经不是 ACTIVE」都要在动手之前拦住。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireActive(String accountId) {
        PropFirmAccount account = accounts.findById(accountId)
                .orElseThrow(() -> new BusinessException("PF 账户不存在，请先创建账户: " + accountId));
        if (!account.isOpen()) {
            throw new BusinessException("PF 账户当前是 " + account.getStatus() + "，不能再产生新的操作");
        }
        return account;
    }

    /**
     * 拿一个<strong>可以下单</strong>的账户。
     *
     * <p>比 {@link #requireActive} 多一条：{@link PropFirmStage#PAYOUT} 阶段虽然"开着"
     * （还在处理出金），但它<strong>是终点，不再接受下单</strong>。画图、录音这类
     * "看盘动作"仍然允许 —— 用户可能想在收工之后再标注一遍。
     */
    @Transactional(readOnly = true)
    public PropFirmAccount requireTradable(String accountId) {
        PropFirmAccount account = requireActive(accountId);
        if (!account.isTradable()) {
            throw new BusinessException("PF 账户已进入 " + account.getStage() + " 阶段，不再接受下单");
        }
        return account;
    }

    /**
     * 删除账户 —— <strong>只删这一行</strong>。
     *
     * <p>它下面的成交、交易记录、语音、操作记录、图形、指标、机器人全部原地保留，
     * 并且仍然记着原来那个 {@code account_id}。想删那些数据，只能由用户一条一条地显式删。
     *
     * <p>阶段流水同样保留（它不是盘面数据，是「这个账户存在过」的记录）。
     *
     * <p>幂等：账户已经不在了就直接返回，不抛异常（删除一个已经删掉的东西，结果就是「它没了」）。
     */
    @Transactional
    public void delete(String accountId) {
        if (!accounts.existsById(accountId)) {
            return;
        }
        accounts.deleteById(accountId);
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
     * <p>失败<strong>不再升级、也不会自动重开</strong> —— 重考要新建一个账户，
     * 这样两次考核的成交天然不会混在一个 {@code accountId} 下。
     */
    @Transactional
    public PropFirmAccount fail(String accountId, PropFirmSession session) {
        PropFirmAccount account = requireActive(accountId);
        closeCurrentStage(account, PropFirmStageLog.EndReason.FAILED, session, Instant.now());
        account.markFailed();
        return accounts.save(account);
    }

    /**
     * 把会话算出的余额 / 峰值写回账户（这两个列是缓存，见 {@code PropFirmAccount#syncComputed}）。
     *
     * <p>推进盘面的那一层每根 K 线之后调一次，界面就能直接读账户行显示
     * 「距爆仓还有多少」，不必每次都重放成交。
     */
    @Transactional
    public PropFirmAccount syncFromSession(String accountId, PropFirmSession session) {
        PropFirmAccount account = accounts.findById(accountId)
                .orElseThrow(() -> new BusinessException("PF 账户不存在: " + accountId));
        account.syncComputed(session.balance(), session.eodPeak());
        return accounts.save(account);
    }

    /**
     * 下单前的手数闸门：这一笔成交之后，该品种的净持仓会不会超过当前利润档位。
     *
     * @param netAfterFill 成交之后该品种的净持仓（买正卖负）
     * @throws BusinessException 超限
     */
    @Transactional(readOnly = true)
    public void checkPositionLimit(String accountId, String symbolId, BigDecimal netAfterFill) {
        PropFirmAccount account = requireTradable(accountId);
        PropFirmPositionLimit.requireWithinLimit(account.getRules(),
                account.netProfitInStage(), symbolId, netAfterFill);
    }

    /** 取一个账户，不判断「能不能往上挂操作」—— 那是 {@link #requireActive} 的事。 */
    @Transactional(readOnly = true)
    public PropFirmAccount get(String accountId) {
        return accounts.findById(accountId)
                .orElseThrow(() -> new BusinessException("PF 账户不存在: " + accountId));
    }

    /**
     * 账户列表，新建的在前。
     *
     * @param status {@code null} = 全部（含 {@code FAILED}）
     */
    @Transactional(readOnly = true)
    public List<PropFirmAccount> list(PropFirmAccount.Status status) {
        return status == null
                ? accounts.findAllByOrderByStartedAtDesc()
                : accounts.findByStatusOrderByStartedAtDesc(status);
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
