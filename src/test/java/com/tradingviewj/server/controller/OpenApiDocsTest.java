package com.tradingviewj.server.controller;

import cn.hutool.json.JSONObject;
import cn.hutool.json.JSONUtil;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

import java.nio.charset.StandardCharsets;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

/**
 * 钉住 Swagger 文档的三条命脉。这三条任何一条断了，页面上都不会报「配置错误」，
 * 只会白屏或者显示一份错的文档，全靠这里拦。
 *
 * <ol>
 *   <li>{@code /v3/api-docs} 不能被统一响应体包住 —— 否则 Swagger UI 找不到顶层 {@code openapi} 字段；</li>
 *   <li>文档里的响应结构必须是 {@code R<T>}，和真实返回一致 —— 否则前端照着文档写出
 *       {@code res.version}（正确写法是 {@code res.data.version}）；</li>
 *   <li>{@code /v3/api-docs/swagger-config} 也要能用 —— UI 靠它才知道去哪个地址拉 spec。</li>
 * </ol>
 */
@SpringBootTest
@AutoConfigureMockMvc
class OpenApiDocsTest {

    @Autowired
    private MockMvc mockMvc;

    private JSONObject fetchJson(String path) throws Exception {
        MvcResult result = mockMvc.perform(get(path))
                .andExpect(status().isOk())
                .andReturn();
        return JSONUtil.parseObj(result.getResponse().getContentAsString(StandardCharsets.UTF_8));
    }

    @Test
    void apiDocsIsNotWrappedByGlobalEnvelope() throws Exception {
        JSONObject doc = fetchJson("/v3/api-docs");

        // 被 GlobalResponseAdvice 包过的特征是顶层出现 code/msg/data。
        // 一条 assert 就能抓住「有人把 org.springdoc 从排除名单里删了」。
        assertThat(doc.containsKey("code")).isFalse();
        assertThat(doc.getStr("openapi")).startsWith("3.");
    }

    @Test
    void swaggerConfigIsNotWrappedEither() throws Exception {
        JSONObject config = fetchJson("/v3/api-docs/swagger-config");

        assertThat(config.containsKey("code")).isFalse();
        assertThat(config.getStr("url")).isEqualTo("/v3/api-docs");
    }

    @Test
    void versionEndpointAppearsInDocument() throws Exception {
        JSONObject doc = fetchJson("/v3/api-docs");
        JSONObject paths = doc.getJSONObject("paths");

        // JSONObject 同时实现了 Map 和 Iterable，assertThat 会重载歧义，这里显式按 Map 断
        assertThat((Map<String, Object>) paths).isNotEmpty();
        assertThat(paths.containsKey("/api/version")).isTrue();
        // paths-to-match 只放 /api/**，文档端点自己不该出现在接口列表里
        assertThat(paths.containsKey("/v3/api-docs")).isFalse();
    }

    /**
     * 核心：文档声明的响应形状必须是 {code, msg, data}，且 data 指向业务模型。
     *
     * <p>Controller 返回的是裸 {@code VersionResponse}，外层包裹是运行期加的；
     * 这条测试保证 {@code OperationCustomizer} 把这个差值补回来了。
     */
    @Test
    void responseSchemaShowsEnvelopeWrappingBusinessData() throws Exception {
        JSONObject doc = fetchJson("/v3/api-docs");
        JSONObject content = doc.getByPath("paths./api/version.get.responses.200.content", JSONObject.class);

        assertThat((Map<String, Object>) content).isNotEmpty();
        // Content-Type 的键由 springdoc 决定（*/* 或 application/json），取第一个即可
        Map.Entry<String, Object> first = content.entrySet().iterator().next();
        JSONObject schema = ((JSONObject) first.getValue()).getJSONObject("schema");
        JSONObject properties = schema.getJSONObject("properties");

        assertThat(properties.keySet()).containsExactlyInAnyOrder("code", "msg", "data");
        assertThat(properties.getJSONObject("data").getStr("$ref"))
                .isEqualTo("#/components/schemas/VersionResponse");
        // 三个字段都必填，前端可以直接省略存在性判断
        assertThat(schema.getJSONArray("required").toList(String.class))
                .containsExactlyInAnyOrder("code", "msg", "data");
    }

    @Test
    void businessModelCarriesFieldDescriptions() throws Exception {
        JSONObject doc = fetchJson("/v3/api-docs");
        JSONObject model = doc.getByPath("components.schemas.VersionResponse", JSONObject.class);

        assertThat((Map<String, Object>) model).isNotEmpty();
        assertThat(model.getJSONObject("properties").getJSONObject("version").getStr("description"))
                .contains("project.version");
    }
}
