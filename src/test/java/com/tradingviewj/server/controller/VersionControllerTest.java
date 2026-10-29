package com.tradingviewj.server.controller;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.web.servlet.MockMvc;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

/**
 * 钉住两件事：接口能返回 1.0.0，且版本号来自 pom 而不是硬编码在 Java 里
 * （Maven 资源过滤失效时这里会变成 "@project.version@" 当场失败）。
 */
@SpringBootTest
@AutoConfigureMockMvc
class VersionControllerTest {

    @Autowired
    private MockMvc mockMvc;

    @Test
    void versionEndpointReturnsProjectVersion() throws Exception {
        mockMvc.perform(get("/api/version"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.version").value("1.0.0"))
                .andExpect(jsonPath("$.application").value("tradingview-j-server"));
    }
}
