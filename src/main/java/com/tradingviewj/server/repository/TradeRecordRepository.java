package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeRecord;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface TradeRecordRepository extends JpaRepository<TradeRecord, String> {

    /** 某个 PF 账户下的全部记录（考核明细）：新的在前。 */
    List<TradeRecord> findByAccountIdOrderByCreatedAtDesc(String accountId);

    /** 某个 PF 账户下的全部记录，按开仓时间排（复盘时按时间顺着看）。 */
    List<TradeRecord> findByAccountIdOrderByEntryBarTimeAsc(String accountId);

    /** 「还没有账户」的记录：只可能是账户被删后留下的，列表里要能单独找出来重新挂。 */
    List<TradeRecord> findByAccountIdIsNull();

    /** 「还在持仓中」的那些（进程崩溃留下的草稿），列表里要单独标出来。 */
    List<TradeRecord> findByClosedFalse();
}
