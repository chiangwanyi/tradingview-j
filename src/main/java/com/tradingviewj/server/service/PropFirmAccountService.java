package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.PropFirmAccountRepository;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.repository.SymbolRepository;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * PF 账户的生命周期：开、删、以及「能不能在这儿动手」。
 *
 * <p>这个类是<strong>两条业务规则的唯一落点</strong>：
 *
 * <ol>
 *   <li><strong>必须先有账户才能操作。</strong> 任何下单、画图、录音、启动机器人的入口，
 *       第一步都该调 {@link #requireActive}。领域方法（各实体的工厂方法）保证「不给账户就
 *       造不出对象」，这个方法保证「给的那个账户真的存在、真的还在考核中」——
 *       两件事都要有，前者防手滑，后者防脏 id（账户已被删、或状态已经 PASSED/FAILED）。</li>
 *   <li><strong>删账户不删数据。</strong> {@link #delete} 只删 {@code prop_firm_account}
 *       这一行。图形、挂单、成交、语音、操作记录、机器人、交易记录、指标<strong>全部保留</strong>，
 *       它们的 {@code account_id} 原样留着（指向一个已经不存在的账户），既不置空也不级联删除。
 *       这里刻意<strong>不</strong>做「顺手清理」：账户是临时统计点，不是这些数据的所有者。</li>
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
    private final SymbolRepository symbols;

    /**
     * 按模板开一个账户。
     *
     * @param templateId 用哪套考核规则（规则会在这一刻复制到账户上）
     * @param name       账户名，用户自己起
     * @param symbolId   这个账户做哪个标的
     * @throws BusinessException 模板或标的不存在
     */
    @Transactional
    public PropFirmAccount open(String templateId, String name, String symbolId) {
        PropFirmTemplate template = templates.findById(templateId)
                .orElseThrow(() -> new BusinessException("模板不存在: " + templateId));
        if (template.getRules() == null || template.getRules().getAccountSize() == null) {
            throw new BusinessException("模板缺少账户规模，开不出账户: " + templateId);
        }
        if (symbols.findById(symbolId).isEmpty()) {
            throw new BusinessException("标的不存在: " + symbolId);
        }
        return accounts.save(PropFirmAccount.open(template, name, symbolId));
    }

    /**
     * 拿一个<strong>可以往上挂操作</strong>的账户，拿不到就抛业务异常。
     *
     * <p>所有交易类入口（下单、画图、录音、启动机器人、开交易记录）都该先过这一关：
     * 「账户不存在」和「账户已经不是 ACTIVE」都要在动手之前拦住，而不是等落库时撞外键。
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
     * 删除账户 —— <strong>只删这一行</strong>。
     *
     * <p>它下面的成交、交易记录、语音、操作记录、图形、指标、机器人全部原地保留，
     * 并且仍然记着原来那个 {@code account_id}。想删那些数据，只能由用户一条一条地显式删。
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

    /** 还在考核中的账户（前端列表用）。 */
    @Transactional(readOnly = true)
    public List<PropFirmAccount> listActive() {
        return accounts.findByStatus(PropFirmAccount.Status.ACTIVE);
    }
}
