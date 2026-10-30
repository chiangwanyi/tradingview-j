package com.tradingviewj.server.controller;

import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.service.PropFirmAccountService;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.http.MediaType;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import java.math.BigDecimal;
import java.util.List;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.ArgumentMatchers.isNull;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.delete;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

/**
 * 账户接口的 JSON 契约：钉住前端 {@code accounts.ts} 依赖的字段名、null 语义和错误码。
 *
 * <p>不起容器、不连库：要验的是「请求体能不能读成 templateId/name」「业务异常会不会
 * 变成 HTTP 200 + code 10000」，那两件事和数据库无关。
 */
@WebMvcTest(PropFirmAccountController.class)
class PropFirmAccountControllerTest {

    @Autowired
    private MockMvc mvc;

    @MockitoBean
    private PropFirmAccountService accounts;

    /** 一个 EVAL 账户：规模 50000、目标 3000，标的 MES。 */
    private static PropFirmAccount evalAccount() {
        PropFirmRules rules = new PropFirmRules(new BigDecimal("50000"), new BigDecimal("3000"),
                new BigDecimal("2000"), PropFirmRules.DrawdownType.TRAILING_EOD,
                new BigDecimal("52100"), new BigDecimal("0.5"), null, null, List.of());
        PropFirmTemplate template = PropFirmTemplate.define("LucidFlex 50K EVAL", null,
                PropFirmStage.EVAL, null, rules);
        return PropFirmAccount.open(template, "CME_MINI:MES1!", "第一次考核",
                "2024-03-04 09:00", "2024-03-04.csv");
    }

    @Test
    void openReadsTemplateIdSymbolIdAndName() throws Exception {
        when(accounts.open(eq("tpl-1"), eq("CME_MINI:MES1!"), eq("第一次考核")))
                .thenReturn(evalAccount());

        String body = """
                {"name":"第一次考核","templateId":"tpl-1","symbolId":"CME_MINI:MES1!"}
                """;

        mvc.perform(post("/api/pf-accounts").contentType(MediaType.APPLICATION_JSON).content(body))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data.stage").value("EVAL"))
                .andExpect(jsonPath("$.data.status").value("ACTIVE"))
                .andExpect(jsonPath("$.data.name").value("第一次考核"))
                // 标的随账户回：交易页不再有标的切换，直接读这个字段
                .andExpect(jsonPath("$.data.symbolId").value("CME_MINI:MES1!"))
                .andExpect(jsonPath("$.data.balance").value(50000.0))
                .andExpect(jsonPath("$.data.startBarTime").value("2024-03-04 09:00"))
                // 规则快照随账户一起回：前端卡片上的目标/回撤直接读它，不再去查模板
                .andExpect(jsonPath("$.data.rules.profitTarget").value(3000.0));

        verify(accounts).open("tpl-1", "CME_MINI:MES1!", "第一次考核");
    }

    /** 实体校验失败（没名字）→ 400，原因原样回给前端。 */
    @Test
    void openMapsIllegalArgumentToBadRequest() throws Exception {
        when(accounts.open(any(), any(), any()))
                .thenThrow(new IllegalArgumentException("账户必须有名字，收到: "));

        mvc.perform(post("/api/pf-accounts").contentType(MediaType.APPLICATION_JSON)
                        .content("{\"name\":\"\",\"templateId\":\"tpl-1\",\"symbolId\":\"CME_MINI:MES1!\"}"))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value(400))
                .andExpect(jsonPath("$.msg").value("账户必须有名字，收到: "));
    }

    /** 业务异常（模板不存在 / 已在 PAYOUT）→ HTTP 200 + code 10000，前端只看 body。 */
    @Test
    void businessErrorIsOkWithBizCode() throws Exception {
        when(accounts.promote(eq("a-1"), isNull()))
                .thenThrow(new BusinessException("PF 账户已在 PAYOUT 阶段，没有下一阶段可升: a-1"));

        mvc.perform(post("/api/pf-accounts/a-1/promote"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(10000))
                .andExpect(jsonPath("$.msg").value("PF 账户已在 PAYOUT 阶段，没有下一阶段可升: a-1"));
    }

    /** 列表带 status 过滤；不传就是全部。 */
    @Test
    void listPassesStatusFilter() throws Exception {
        when(accounts.list(PropFirmAccount.Status.FAILED)).thenReturn(List.of());

        mvc.perform(get("/api/pf-accounts").param("status", "FAILED"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data").isArray());

        verify(accounts).list(PropFirmAccount.Status.FAILED);
    }

    /** 删除：成功但没有数据 —— data 字段仍在（R 上单独标了 ALWAYS）。 */
    @Test
    void deleteReturnsEnvelopeWithNullData() throws Exception {
        mvc.perform(delete("/api/pf-accounts/a-1"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data").doesNotExist());
    }
}
