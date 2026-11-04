/**
 * 接口出入口对象（DTO）：Controller 收的请求体、返回的响应体。
 *
 * <p>和 {@code entity} 严格分开：{@code entity} 是<strong>库里的东西</strong>，
 * 这里是<strong>网线上的东西</strong>。把实体直接当响应体返回，就等于让数据库表的
 * 列名变成对外的 API 契约 —— 之后想改一个列名、拆一张表，都会变成一个破坏性变更。
 *
 * <p>本包只允许 import JDK、Lombok 和 Swagger 注解；<strong>不 import entity</strong>
 * 是理想状态，需要转换时在 service 里做。
 *
 * <p>已有一处例外：{@link com.tradingviewj.server.dto.PropFirmTemplateRequest} 直接复用
 * {@code PropFirmRules} / {@code PropFirmStage} —— 规则的字段名本身就是对外契约（前端
 * {@code webapp/src/data/rules.ts} 对着它写），镜像一份出来只会立刻漂移。
 */
package com.tradingviewj.server.dto;
