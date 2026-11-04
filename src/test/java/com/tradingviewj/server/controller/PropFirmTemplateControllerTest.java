package com.tradingviewj.server.controller;

import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.service.PropFirmTemplateService;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.http.MediaType;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import java.math.BigDecimal;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.ArgumentMatchers.isNull;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

/**
 * 模板接口的 JSON 契约：钉住前端 {@code rules.ts} 依赖的字段名与 null 语义。
 *
 * <p>不起 Spring 容器、不连库（service 被替换成 mock）：这里要验的是「请求体能不能读进
 * {@link PropFirmRules}、错误原因会不会原样回给前端」，那两件事跟数据库无关。
 */
@WebMvcTest(PropFirmTemplateController.class)
class PropFirmTemplateControllerTest {

    @Autowired
    private MockMvc mvc;

    @MockitoBean
    private PropFirmTemplateService templates;

    private static PropFirmRules fundedRules() {
        return new PropFirmRules(new BigDecimal("50000"), null, new BigDecimal("2000"),
                PropFirmRules.DrawdownType.TRAILING_EOD, new BigDecimal("52100"), null, 5,
                new BigDecimal("150"), List.of());
    }

    @Test
    void create_readsRulesFromJsonAndKeepsMeaningfulNulls() throws Exception {
        PropFirmTemplate saved = PropFirmTemplate.define("LucidFlex 50K FUNDED", "出金号",
                PropFirmStage.FUNDED, null, fundedRules());
        when(templates.create(anyString(), any(), eq(PropFirmStage.FUNDED), isNull(), any())).thenReturn(saved);

        String body = """
                {
                  "name": "LucidFlex 50K FUNDED",
                  "description": "出金号",
                  "stage": "FUNDED",
                  "nextTemplateId": null,
                  "rules": {
                    "accountSize": 50000,
                    "profitTarget": null,
                    "maxDrawdown": 2000,
                    "drawdownType": "TRAILING_EOD",
                    "trailingLockLevel": 52100,
                    "consistencyRatio": null,
                    "minProfitDays": 5,
                    "profitDayThreshold": 150,
                    "positionTiers": [{"fromProfit": 0, "maxContracts": 20}]
                  }
                }
                """;

        mvc.perform(post("/api/pf-templates").contentType(MediaType.APPLICATION_JSON).content(body))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data.stage").value("FUNDED"))
                .andExpect(jsonPath("$.data.id").isNotEmpty())
                // non_null 把 null 字段整个省掉：前端读到的是 undefined，不是 null
                .andExpect(jsonPath("$.data.rules.profitTarget").doesNotExist());

        ArgumentCaptor<PropFirmRules> captor = ArgumentCaptor.forClass(PropFirmRules.class);
        verify(templates).create(eq("LucidFlex 50K FUNDED"), eq("出金号"), eq(PropFirmStage.FUNDED),
                isNull(), captor.capture());
        PropFirmRules rules = captor.getValue();
        assertNull(rules.getProfitTarget(), "FUNDED 的 profitTarget 必须读成 null，不是 0");
        assertEquals(0, new BigDecimal("50000").compareTo(rules.getAccountSize()));
        assertEquals(5, rules.getMinProfitDays());
        assertEquals(1, rules.getPositionTiers().size(), "手数分档要能从 JSON 读进来");
        assertEquals(20, rules.getPositionTiers().get(0).maxContracts());
    }

    @Test
    void create_mapsEntityValidationToBadRequest() throws Exception {
        when(templates.create(any(), any(), any(), any(), any()))
                .thenThrow(new IllegalArgumentException("EVAL 阶段必须有盈利目标: x"));

        String body = """
                {"name":"x","stage":"EVAL","rules":{"accountSize":50000,"maxDrawdown":2000}}
                """;

        mvc.perform(post("/api/pf-templates").contentType(MediaType.APPLICATION_JSON).content(body))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value(400))
                .andExpect(jsonPath("$.msg").value("EVAL 阶段必须有盈利目标: x"));
    }
}
