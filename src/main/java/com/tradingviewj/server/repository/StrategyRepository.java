package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.strategy.Strategy;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

public interface StrategyRepository extends JpaRepository<Strategy, String> {

    /** 启动面板按 code（bean 名）拿策略；同步注册表时也用它判重。 */
    Optional<Strategy> findByCode(String code);

    List<Strategy> findByEnabledTrue();
}
