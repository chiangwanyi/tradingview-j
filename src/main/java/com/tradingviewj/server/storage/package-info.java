/**
 * 文件读写层（预留，当前无实现）。
 *
 * <p>规划位置：CSV / 录像 / 配置文件这类「本地磁盘上的东西」都从这里进出，例如
 * {@code data/*.csv}、{@code recordings/*.json}、{@code state/*.json}。
 *
 * <p>两条纪律：路径一律由调用方传进来（不在这里硬编码 {@code data/}、{@code raw/}），
 * 写文件一律「先写临时文件再 rename」，避免中途崩溃留下半个文件——和 Go 版当前的做法一致。
 *
 * <p>只允许 import JDK 与 {@code entity}；不注入 service / repository，保持单向依赖。
 */
package com.tradingviewj.server.storage;
