package com.tradingviewj.server.config;

import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

/**
 * 应用级配置（对应 Go 版本的 config.json）。
 *
 * <p>版本号不硬编码在 Java 代码里：由 pom.xml 的 project.version 经 Maven 资源过滤注入
 * application.yml，再绑定到这里。升版本只改一处。
 */
@Getter
@Setter
@Component
@ConfigurationProperties(prefix = "app")
public class AppProperties {

    /** 应用版本号，例如 1.0.0。 */
    private String version = "1.0.0";

    /** 应用名。 */
    private String name = "tradingview-j-server";

    /**
     * K 线 CSV 的根目录（对应 Go 版 config.json 的 {@code outDir}）。
     *
     * <p>只有这一项走配置：数据库路径由 {@code spring.datasource.url} 管，
     * 两者分开是因为它们的生命周期不同 —— 行情会被清、会被重新抓，业务库不会。
     */
    private String barRoot = "./data";
}
