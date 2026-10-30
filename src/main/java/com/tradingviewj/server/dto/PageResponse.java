package com.tradingviewj.server.dto;

import java.util.List;

/**
 * 一页数据：{@code items} + 总数。
 *
 * <p>不直接返回 Spring Data 的 {@code Page}：它的 JSON 里带着 {@code pageable} /
 * {@code sort} / {@code first} / {@code numberOfElements} 这一堆 Spring 内部结构，
 * 前端只需要「这页有哪些 + 一共多少条」。
 *
 * <p>{@code page} 页码<strong>从 0 起</strong>（和后端 {@code PageRequest} 一致，不做 +1 换算）。
 */
public record PageResponse<T>(List<T> items, long total, int page, int size) {
}
