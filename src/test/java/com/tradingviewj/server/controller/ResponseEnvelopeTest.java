package com.tradingviewj.server.controller;

import cn.hutool.json.JSONObject;
import cn.hutool.json.JSONUtil;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

/**
 * 钉住「成功和失败返回结构完全一致」这条约定。
 *
 * <p>被测试的接口在 {@link DemoApiController}（仅测试源码内存在）。
 */
@SpringBootTest
@AutoConfigureMockMvc
class ResponseEnvelopeTest {

    @Autowired
    private MockMvc mockMvc;

    /** 普通对象：自动包一层。 */
    @Test
    void objectReturnIsWrapped() throws Exception {
        mockMvc.perform(get("/test/object"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.msg").value("ok"))
                .andExpect(jsonPath("$.data.version").value("9.9.9"));
    }

    /** String 返回值也不会掉进 StringHttpMessageConverter 的坑，仍然是同一个结构。 */
    @Test
    void stringReturnIsWrappedAsJson() throws Exception {
        mockMvc.perform(get("/test/string"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data").value("hello"));
    }

    /** Controller 自己包过的，不会变成 data 里再套一个 code/msg。 */
    @Test
    void alreadyWrappedBodyIsNotWrappedTwice() throws Exception {
        MvcResult result = mockMvc.perform(get("/test/wrapped"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(0))
                .andExpect(jsonPath("$.data.k").value("v"))
                .andReturn();

        JSONObject json = JSONUtil.parseObj(result.getResponse().getContentAsString());
        assertThat(json.getJSONObject("data").containsKey("code")).isFalse();
    }

    /** 业务异常：HTTP 200 + 业务码，字段一个不少。 */
    @Test
    void businessExceptionKeepsEnvelopeWithHttp200() throws Exception {
        MvcResult result = mockMvc.perform(get("/test/biz-error"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value(10001))
                .andExpect(jsonPath("$.msg").value("余额不足"))
                .andReturn();

        // data 为 null，但字段必须在（default-property-inclusion=non_null 会把裸 null 字段吞掉）
        assertThat(result.getResponse().getContentAsString()).contains("\"data\":null");
    }

    /** 未预料的异常：HTTP 500，结构不变，且不把内部细节漏给调用方。 */
    @Test
    void unexpectedExceptionIsMaskedAndKeepsEnvelope() throws Exception {
        MvcResult result = mockMvc.perform(get("/test/boom"))
                .andExpect(status().isInternalServerError())
                .andExpect(jsonPath("$.code").value(500))
                .andExpect(jsonPath("$.msg").value("服务内部错误"))
                .andReturn();

        String body = result.getResponse().getContentAsString();
        assertThat(body).doesNotContain("db down");
        assertThat(body).contains("\"data\":null");
    }

    /** 参数类型不匹配：HTTP 400，还是同一个结构。 */
    @Test
    void typeMismatchReturnsBadRequestEnvelope() throws Exception {
        MvcResult result = mockMvc.perform(get("/test/num/abc"))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value(400))
                .andReturn();

        assertThat(result.getResponse().getContentAsString()).contains("参数 n 类型不正确");
    }

    /** 路径不存在：不能落到 /error 端点去，否则结构和别处不一样。 */
    @Test
    void unmappedPathReturnsNotFoundEnvelope() throws Exception {
        mockMvc.perform(get("/test/no-such-path"))
                .andExpect(status().isNotFound())
                .andExpect(jsonPath("$.code").value(404))
                .andExpect(jsonPath("$.msg").value("资源不存在"));
    }

    /** 示例：Controller 里用 Hutool 做对象转换，返回的仍是业务对象。 */
    @Test
    void hutoolBeanConversionInsideController() throws Exception {
        mockMvc.perform(get("/test/bean"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.label").value("demo-9.9.9"))
                .andExpect(jsonPath("$.data.application").value("demo"));
    }
}
