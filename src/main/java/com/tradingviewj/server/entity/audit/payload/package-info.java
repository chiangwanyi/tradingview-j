/**
 * 一个复盘动作<strong>带什么负载</strong>的类型清单：一个动作一个 record、一个文件。
 *
 * <p>对应 Go 版 {@code internal/replay/protocol.go} 里「广播负载」那一段 —— 那边是一个
 * 结构体一个动作，这里是一个 record 一个动作。拆开只为一件事：想看清「
 * {@code ORDER_FILLED} 到底广播了什么」时，打开一个几十行的文件就够，不必在一张两百行
 * 的大结构里挑字段。
 *
 * <p><strong>它们不参与持久化，也不是 Jackson 绑定层。</strong>
 * {@code audit_event.payload_json} 是 Go 写进来的原文（{@code TEXT}），Java 侧只存不解析 ——
 * 见 {@link com.tradingviewj.server.entity.audit.AuditEvent} 的「payload 为什么是字符串」。
 * 这里给的是「按 action 挑哪一类来解析」的那个落点，以及一份可读的结构说明。
 *
 * <p>字段映射的固定规则（wire → 这里的 Java）：
 *
 * <ol>
 *   <li>金额与手数：{@code float64} → {@code BigDecimal}（全库口径，见
 *       {@code entity} 包说明第 4 条）；</li>
 *   <li>K 线时间：wire 的 {@code time} ↔ Java 的 {@code Bar.barTime}，都是本地时区明文；</li>
 *   <li>方向：wire 的 {@code "buy"/"sell"}、{@code "long"/"short"} 字符串 ↔ {@link
 *       com.tradingviewj.server.entity.trade.Side} 枚举；</li>
 *   <li>Go 的指针字段（{@code *Order}）可空 ↔ Java 允许 {@code null} 的引用；
 *       其余集合一律非 {@code null}（wire 上数组永远不是 {@code null}）。</li>
 * </ol>
 *
 * <p>{@code BASE} 与 {@code RECORDING_AUDIO} 没有负载（起点就是盘面本身，语音落在那一步的
 * {@code Audio} 上）。仍然给空 record，是为了让 {@code switch} 覆盖全部 20 个动作，
 * 不必再写一个「这两个没有负载」的旁支。
 *
 * <p>另有两个<strong>支撑类型</strong>（不是动作）：{@link
 * com.tradingviewj.server.entity.audit.payload.VoiceState} 与 {@link
 * com.tradingviewj.server.entity.audit.payload.Member}。它们被多条动作共用，
 * 所以不塞进某一个动作的文件里。
 */
package com.tradingviewj.server.entity.audit.payload;
