package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.trade.Trade;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

/**
 * 成交仓库。
 *
 * <p>所有查询都<strong>必须带排序</strong>：持仓和盈亏是靠按时间重放算出来的，
 * 顺序错了结果就是错的，而且是那种「数字看着像对的」的错。
 *
 * <p>排序第二级是 {@code seq}（成交流水号），<strong>不是 id</strong>：id 是 UUID，
 * 同一根 K 线上多笔成交按它排等于随机排。
 */
public interface TradeRepository extends JpaRepository<Trade, String> {

    List<Trade> findByAccountIdOrderByBarTimeAscSeqAsc(String accountId);

    List<Trade> findByAccountIdAndSymbolIdOrderBySeqAsc(String accountId, String symbolId);

    List<Trade> findByTradeRecordIdOrderByBarTimeAscSeqAsc(String tradeRecordId);

    long countByAccountId(String accountId);

    /** 下一笔成交的流水号。单账户单写者，不需要锁。 */
    Optional<Trade> findTopByAccountIdOrderBySeqDesc(String accountId);
}
