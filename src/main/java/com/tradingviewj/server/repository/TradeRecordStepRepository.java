package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeRecordStep;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

public interface TradeRecordStepRepository extends JpaRepository<TradeRecordStep, String> {

    /** 打开一条记录：按步号取，回放时一步一步演。 */
    List<TradeRecordStep> findByRecordIdOrderByStepNoAsc(String recordId);

    /** 下一条的步号 = 它的步号 + 1。 */
    Optional<TradeRecordStep> findTopByRecordIdOrderByStepNoDesc(String recordId);
}
