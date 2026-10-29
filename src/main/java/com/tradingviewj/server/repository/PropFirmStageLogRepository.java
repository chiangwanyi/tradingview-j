package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

/** 阶段流水的查询：按账户取全部（UI 画时间轴）或只取当前正在走的那一段。 */
public interface PropFirmStageLogRepository extends JpaRepository<PropFirmStageLog, String> {

    /** 这个账户当过的所有阶段，按进入顺序。 */
    List<PropFirmStageLog> findByAccountIdOrderBySeqAsc(String accountId);

    /** 最后一次进入的阶段 = 当前阶段。 */
    Optional<PropFirmStageLog> findTopByAccountIdOrderBySeqDesc(String accountId);

    /** 还开着的那一行（{@code endedAt} 为空）。正常情况最多一条。 */
    Optional<PropFirmStageLog> findFirstByAccountIdAndEndedAtIsNullOrderBySeqDesc(String accountId);
}
