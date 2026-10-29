/**
 * 文件读写层：K 线 CSV、语音 WAV 这类「本地磁盘上的东西」。
 *
 * <p>和 {@code repository}（数据库）严格分工：<strong>凡是能放进关系表的事实都进库</strong>，
 * 只有「体量大 + 人类可读 + 外部程序也在读写」的三类东西留在文件里 ——
 * 目前就是 K 线（{@code data/}）和音频（{@code recordings/} 下的 wav）。
 *
 * <p>两条纪律：
 *
 * <ul>
 *   <li>路径一律由调用方或配置传进来（不在这里硬编码 {@code data/}、{@code raw/}）；</li>
 *   <li>写文件一律「先写临时文件再 rename」，避免中途崩溃留下半个文件。</li>
 * </ul>
 *
 * <p>只允许 import JDK、{@code config} 与 {@code entity}；不注入 service / repository，
 * 保持单向依赖。
 */
package com.tradingviewj.server.storage;
