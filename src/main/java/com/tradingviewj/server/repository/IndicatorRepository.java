package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.indicator.Indicator;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface IndicatorRepository extends JpaRepository<Indicator, String> {

    List<Indicator> findByAccountIdOrderBySortOrderAsc(String accountId);
}
