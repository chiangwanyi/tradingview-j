package com.tradingviewj.server;

import lombok.extern.slf4j.Slf4j;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.core.env.Environment;

/**
 * 程序入口。
 *
 * <p>对应 Go 版本里的 main.go：只负责启动，不做业务编排。
 */
@Slf4j
@SpringBootApplication
public class ServerApplication {

    public static void main(String[] args) {
        Environment env = SpringApplication.run(ServerApplication.class, args).getEnvironment();
        String port = env.getProperty("local.server.port", env.getProperty("server.port", "8080"));
        // 日志一律写英文：和 Go 版本的日志风格保持一致，便于迁移期对照排查
        log.info("tradingview-j server started, version={}, port={}", env.getProperty("app.version", "unknown"), port);
        log.info("version endpoint: http://127.0.0.1:{}/api/version", port);
    }
}
