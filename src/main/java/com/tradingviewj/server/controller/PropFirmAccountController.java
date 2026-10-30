package com.tradingviewj.server.controller;

import com.tradingviewj.server.api.R;
import com.tradingviewj.server.dto.PropFirmAccountRequest;
import com.tradingviewj.server.dto.PropFirmCursorRequest;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import com.tradingviewj.server.service.PropFirmAccountService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * PF 账户接口：{@code /api/pf-accounts}。
 *
 * <p>和模板接口一样只做转发：账户的规则（能不能开、能不能下单、升到哪）全在
 * {@link PropFirmAccountService} 和实体的工厂方法里，这里再写一遍就会出现第二份真相。
 *
 * <p>返回实体不是 DTO：账户的字段集就是它的 API 契约。注意 {@code application.yml} 的
 * {@code non_null} 会把取值为 null 的字段整个省掉（前端读到 {@code undefined}），
 * 所以前端一律用 {@code == null} 判断。
 *
 * <p>业务操作只有三个：升级（{@code promote}）、判失败（{@code fail}）、
 * 步进（{@code cursor}，只把「盘面最新时间」往后推一格）。其余都是读写。
 * 没有「重置余额」—— 余额只能由成交重算后同步，手工改余额会让账和成交流水对不上。
 */
@Tag(name = "PF 账户", description = "考核账户的增删改查，以及升级 / 判失败两个业务动作")
@RestController
@RequestMapping("/api/pf-accounts")
@RequiredArgsConstructor
public class PropFirmAccountController {

    private final PropFirmAccountService accounts;

    @Operation(summary = "账户列表", description = """
            status 可选：ACTIVE / FAILED，不传 = 全部。按建户时间倒序，新建的在前。
            每个元素都带 rules 快照（规则是开账户时复制的，不是指向模板的引用）。
            """)
    @GetMapping
    public List<PropFirmAccount> list(
            @RequestParam(required = false) PropFirmAccount.Status status) {
        return accounts.list(status);
    }

    @Operation(summary = "取一个账户", description = "不存在时返回业务错误码 10000。")
    @GetMapping("/{id}")
    public PropFirmAccount get(@PathVariable String id) {
        return accounts.get(id);
    }

    @Operation(summary = "开一个账户", description = """
            templateId / name / symbolId 三个都必填，规则在这一刻复制进账户。
            symbolId 是账户唯一可交易的标的，**建户后不可改**；起点 K 线只在它的日文件里抽
            （当天 09:00 及之后第一根），抽不到就不建账户（业务错误码 10000）。
            阶段由模板决定：FUNDED 模板建出来的账户直接是 FUNDED。
            """)
    @PostMapping
    public PropFirmAccount open(@RequestBody PropFirmAccountRequest body) {
        return accounts.open(body.templateId(), body.symbolId(), body.name());
    }

    @Operation(summary = "改名", description = """
            只改 name；请求体里的 templateId / symbolId 被忽略
            （换模板或换标的都等于重开一个账户，标的在建户时就锁死了）。
            失败 / PAYOUT 的账户也能改名 —— 它只是标签，不影响余额和阶段流水。
            """)
    @PutMapping("/{id}")
    public PropFirmAccount rename(@PathVariable String id, @RequestBody PropFirmAccountRequest body) {
        return accounts.rename(id, body.name());
    }

    @Operation(summary = "删账户", description = """
            幂等。只删 prop_firm_account 这一行：成交、交易记录、图形、语音、阶段流水全部保留，
            它们的 account_id 原样留着（指向一个已经不存在的账户）。
            """)
    @DeleteMapping("/{id}")
    public R<Void> delete(@PathVariable String id) {
        accounts.delete(id);
        return R.ok();
    }

    @Operation(summary = "升级到下一阶段", description = """
            EVAL → FUNDED 时按模板的 nextTemplateId 换规则、把余额重置回新账户规模；
            FUNDED → PAYOUT 就到尽头。每次升级写一行阶段流水（reason=PROMOTED），
            并把上一段结掉。已在 PAYOUT / 已失败 / 模板没配下一套 → 业务错误码 10000。
            """)
    @PostMapping("/{id}/promote")
    public PropFirmAccount promote(@PathVariable String id) {
        return accounts.promote(id, null);
    }

    @Operation(summary = "判失败", description = """
            撞了亏损线：账户置 FAILED（终态，之后不接受任何操作），当前阶段流水记 FAILED。
            失败不会自动重开 —— 重考要新建账户，这样两次考核的成交不会混在一个 accountId 下。
            """)
    @PostMapping("/{id}/fail")
    public PropFirmAccount fail(@PathVariable String id) {
        return accounts.fail(id, null);
    }

    @Operation(summary = "步进一根（记录盘面最新时间）", description = """
            把账户的 lastBarTime 推到你给的那一根，交易页下次「继续交易」就揭示到那里。
            必须 yyyy-MM-dd HH:mm，而且只能往后走（退回去要回滚账，现在不做）。
            账户已失败 / 不存在 → 业务错误码 10000。
            """)
    @PostMapping("/{id}/cursor")
    public PropFirmAccount cursor(@PathVariable String id,
                                 @RequestBody PropFirmCursorRequest body) {
        return accounts.advanceCursor(id, body.barTime());
    }

    @Operation(summary = "阶段流水", description = """
            这个账户当过的所有阶段，按进入顺序。行数就是身份：
            1 行 = 建户时就在这个阶段；2 行 = 考过来的。
            """)
    @GetMapping("/{id}/stages")
    public List<PropFirmStageLog> stages(@PathVariable String id) {
        return accounts.stageHistory(id);
    }
}
