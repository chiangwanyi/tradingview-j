package com.tradingviewj.server.dto;

import io.swagger.v3.oas.annotations.media.Schema;

/** GET /api/pf-accounts/random-name 的响应体：一个还没被用过的名字。 */
@Schema(description = "随机账户名")
public record RandomNameResponse(@Schema(description = "随机名，如「宁静山谷」", example = "宁静山谷") String name) {
}
