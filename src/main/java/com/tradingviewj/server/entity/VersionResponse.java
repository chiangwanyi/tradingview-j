package com.tradingviewj.server.entity;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * GET /api/version 的响应体。
 *
 * <p>字段全部显式声明，避免 Lombok 之外再引入 Jackson 的隐式行为；
 * 对应 Go 版本里 JSON 结构体的 json tag。
 *
 * <p>注意这是「外层包裹里 data 字段的内容」，不是整个响应。整个响应形如
 * {@code {"code":0,"msg":"ok","data":{"version":"1.0.0","application":"..."}}}，
 * 外壳由 {@link com.tradingviewj.server.config.OpenApiConfig} 在文档里补上。
 */
@Schema(description = "版本信息（统一响应体里的 data 部分）")
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
public final class VersionResponse {

    /** 版本号，例如 1.0.0。 */
    @Schema(description = "版本号，来自 pom.xml 的 project.version", example = "1.0.0")
    private String version;

    /** 应用名。 */
    @Schema(description = "应用名", example = "tradingview-j-server")
    private String application;
}
