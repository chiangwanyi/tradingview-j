package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.strategy.StrategyBot;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

public interface StrategyBotRepository extends JpaRepository<StrategyBot, String> {

    List<StrategyBot> findByAccountIdOrderByStartedAtDesc(String accountId);

    /** 这个账户上还在跑的机器人（最多一个，但用 List 避免为了「最多一个」写唯一约束）。 */
    List<StrategyBot> findByAccountIdAndStatus(String accountId, StrategyBot.Status status);

    Optional<StrategyBot> findFirstByAccountIdAndStatusOrderByStartedAtDesc(String accountId,
                                                                          StrategyBot.Status status);
}
