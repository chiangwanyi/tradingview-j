package com.tradingviewj.server.controller;

import com.tradingviewj.server.dto.VersionResponse;
import com.tradingviewj.server.service.VersionService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * 版本号接口：GET /api/version。
 *
 * <p>第一个 Controller，用来确认「服务能起来、日志能打、配置能读」这条链路是通的。
 *
 * <p>注意返回值是裸的 {@link VersionResponse}，不是 {@code R<VersionResponse>}：
 * 外层 {@code code/msg/data} 由 {@link com.tradingviewj.server.web.GlobalResponseAdvice} 统一加。
 * 这里要写的是「这个接口返回什么业务数据」，不是「怎么包一层」。
 *
 * <p>文档注解（{@code @Tag} / {@code @Operation}）只描述「这个接口是干什么的、怎么调」，
 * 响应里的 code/msg/data 由 {@link com.tradingviewj.server.config.OpenApiConfig} 统一补上，
 * 所以方法签名上不用重复写 {@code @ApiResponse}。
 */
@Slf4j
@Tag(name = "系统", description = "服务自身信息，不需要登录")
@RestController
@RequestMapping("/api")
@RequiredArgsConstructor
public class VersionController {

    private final VersionService versionService;

    @Operation(
            summary = "查询版本号",
            description = """
                    返回当前运行的服务版本号和应用名。

                    版本号不是硬编码的：pom.xml 的 project.version 经 Maven 资源过滤写进
                    application.yml 的 app.version，再绑定到 AppProperties。升版本只改 pom 一处。

                    常用于部署后探活：能返回 200 且版本号符合预期，说明服务已经起来了。
                    """)
    @GetMapping("/version")
    public VersionResponse version() {
        VersionResponse response = versionService.currentVersion();
        log.debug("GET /api/version -> {}", response.getVersion());
        return response;
    }
}
