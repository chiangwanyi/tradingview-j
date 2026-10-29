/**
 * 策略输入：喂进去的 K 线、当前持仓、以及一次问询拿到的全部上下文。
 *
 * <p>`StrategyContext.bars` 只含**已揭示**的 K 线，`cursor` 指向刚收盘那根——别把未揭示的算进去。
 */
package com.tradingviewj.server.entity.strategy.input;
