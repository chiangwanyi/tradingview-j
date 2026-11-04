/**
 * 持久化边界的类型适配层：JPA {@code AttributeConverter}。
 *
 * <p>只放「Java 类型 ↔ SQLite 列类型」这一层翻译，不放任何业务判断。
 * 它服务于{@code entity} 包，但<strong>不属于</strong>实体包 —— 实体描述业务上是什么，
 * 它描述 SQLite 存得下什么。两者变化的原因不同，所以分开放。
 *
 * <p>目前只有两个，都是被 SQLite 的类型缺失逼出来的：
 *
 * <ul>
 *   <li>{@link com.tradingviewj.server.converter.BigDecimalTextConverter} ——
 *       没有 decimal 类型，金额必须走 TEXT，否则 NUMERIC 亲和性会把值当 REAL 存。</li>
 *   <li>{@link com.tradingviewj.server.converter.InstantEpochConverter} ——
 *       没有日期类型，默认映射落成字符串，排序会退化成字符串比较。</li>
 * </ul>
 *
 * <p><strong>枚举不再有转换器。</strong>{@code Actor} / {@code ReplayAction} 用
 * {@code @Enumerated(EnumType.STRING)}，库里存的就是 Java 常量名（{@code HUMAN}、
 * {@code BAR_ADVANCED}）。协议上的小写串（{@code human}、{@code bar_advanced}）由
 * 输出侧（DTO / 前端）自己映射，持久化层不为展示格式让步。
 */
package com.tradingviewj.server.converter;
