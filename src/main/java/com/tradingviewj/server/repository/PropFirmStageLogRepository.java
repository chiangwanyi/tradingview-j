package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmStageLog;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

/** 阶段流水的查询：按账户取全部（UI 画时间轴）或只取当前正在走的那一段。 */
public interface PropFirmStageLogRepository extends JpaRepository<PropFirmStageLog, String> {

    /** 这个账户当过的所有阶段，按进入顺序。 */
    List<PropFirmStageLog> findByAccountIdOrderBySeqAsc(String accountId);

    /**
     * 删掉这个账户的全部流水，返回删了几行。
     *
     * <p>只有 {@code PropFirmAccountService#delete} 会调它 —— <strong>流水跟着账户走</strong>，
     * 不单独提供「删某条流水」的入口（时间轴中间挖一个洞，行数就不再说明身份了）。
     *
     * <p>返回行数是为了让调用方能区分「这个 id 本来就没有流水」和「清了 2 行」，
     * 顺带让重复删除天然幂等（第二次删 0 行）。
     */
    long deleteByAccountId(String accountId);

    /** 最后一次进入的阶段 = 当前阶段。 */
    Optional<PropFirmStageLog> findTopByAccountIdOrderBySeqDesc(String accountId);

    /** 还开着的那一行（{@code endedAt} 为空）。正常情况最多一条。 */
    Optional<PropFirmStageLog> findFirstByAccountIdAndEndedAtIsNullOrderBySeqDesc(String accountId);
}
