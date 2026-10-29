package com.tradingviewj.server.api;

import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * GET /api/version 的响应体。
 *
 * <p>字段全部显式声明，避免 Lombok 之外再引入 Jackson 的隐式行为；
 * 对应 Go 版本里 JSON 结构体的 json tag。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public class VersionResponse {

    /** 版本号，例如 1.0.0。 */
    private String version;

    /** 应用名。 */
    private String application;
}
