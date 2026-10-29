package com.tradingviewj.server.controller;

import com.tradingviewj.server.api.VersionResponse;
import com.tradingviewj.server.config.AppProperties;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * 版本号接口：GET /api/version。
 *
 * <p>第一个 Controller，用来确认「服务能起来、日志能打、配置能读」这条链路是通的。
 */
@Slf4j
@RestController
@RequestMapping("/api")
@RequiredArgsConstructor
public class VersionController {

    private final AppProperties appProperties;

    @GetMapping("/version")
    public VersionResponse version() {
        String version = appProperties.getVersion();
        log.debug("GET /api/version -> {}", version);
        return VersionResponse.builder()
                .version(version)
                .application(appProperties.getName())
                .build();
    }
}
