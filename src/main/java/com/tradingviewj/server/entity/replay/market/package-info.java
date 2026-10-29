/**
 * 行情：K 线、坐标点、周期、标的信息。
 *
 * <p>送给前端的一根 K 线（`Bar`）与标注坐标（`Point`）都在这里；`BarAdvancedPayload` / `BarExtendedPayload`
 * 虽然名字带 Payload，但它就是「推进行情」这件事的产物，跟着行情走而不是单独堆一个消息包。
 */
package com.tradingviewj.server.entity.replay.market;
