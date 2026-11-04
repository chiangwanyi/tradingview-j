package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.trade.TradeOrder;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface OrderRepository extends JpaRepository<TradeOrder, String> {

    List<TradeOrder> findByAccountIdOrderByPlacedAtBarAsc(String accountId);

    /** 进快照时只需要还挂着的单。 */
    List<TradeOrder> findByAccountIdAndStatusOrderByPlacedAtBarAsc(String accountId, TradeOrder.Status status);
}
