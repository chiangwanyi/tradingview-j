package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.trade.Order;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface OrderRepository extends JpaRepository<Order, String> {

    List<Order> findByAccountIdOrderByPlacedAtBarAsc(String accountId);

    /** 进快照时只需要还挂着的单。 */
    List<Order> findByAccountIdAndStatusOrderByPlacedAtBarAsc(String accountId, Order.Status status);
}
