package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.trade.Trade;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/**
 * 成交仓库。
 *
 * <p>所有查询都<strong>必须带排序</strong>：持仓和盈亏是靠按时间重放算出来的，
 * 顺序错了结果就是错的，而且是那种「数字看着像对的」的错。
 */
public interface TradeRepository extends JpaRepository<Trade, String> {

    List<Trade> findByAccountIdOrderByBarTimeAscIdAsc(String accountId);

    List<Trade> findByTradeRecordIdOrderByBarTimeAscIdAsc(String tradeRecordId);

    long countByAccountId(String accountId);
}
