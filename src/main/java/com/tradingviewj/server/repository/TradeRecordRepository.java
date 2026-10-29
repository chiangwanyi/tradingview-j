package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeRecord;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface TradeRecordRepository extends JpaRepository<TradeRecord, String> {

    /** 记录列表：新的在前。 */
    List<TradeRecord> findBySessionIdOrderByCreatedAtDesc(String sessionId);

    /** 某个 PF 账户下的全部记录（考核明细）。 */
    List<TradeRecord> findByAccountIdOrderByEntryBarTimeAsc(String accountId);

    /** 「还在持仓中」的那些（进程崩溃留下的草稿），列表里要单独标出来。 */
    List<TradeRecord> findByClosedFalse();
}
