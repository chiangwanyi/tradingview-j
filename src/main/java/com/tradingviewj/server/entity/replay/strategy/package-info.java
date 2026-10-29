/**
 * 「有哪些策略可以跑」的协议形态（对应 Go 的 `replay.StrategyInfo`）。
 *
 * <p>注意与 `entity.strategy.output.StrategyInfo` 区分：那个是策略层的描述（Go 的 `strategy.Info`），
 * 这个是发给前端的协议形态，字段多了 `desc` 和 Jackson 注解。
 */
package com.tradingviewj.server.entity.replay.strategy;
