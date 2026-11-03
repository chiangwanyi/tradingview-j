package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmSettlement;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/** 日终结算记录的查询：按「账户 + 阶段」取全部，按交易日升序重放。 */
public interface PropFirmSettlementRepository extends JpaRepository<PropFirmSettlement, String> {

    /**
     * 这个账户在当前阶段走过的每一天的判决，按交易日升序。
     *
     * <p>升序是<strong>必须</strong>的：重放时逐日套用峰值，只有顺序对了「只增不减」才成立。
     * 交易日字符串是定长 {@code yyyy-MM-dd}，字典序 = 时间序。
     */
    List<PropFirmSettlement> findByAccountIdAndStageOrderByTradingDayAsc(String accountId, PropFirmStage stage);

    /**
     * 删掉这个账户的全部结算记录，返回删了几行。
     *
     * <p>只有 {@code PropFirmAccountService#delete} 会调它 —— 结算记录和阶段流水一样，
     * 记的是<strong>账户自己</strong>的时间轴，账户没了就没有任何入口能读到。
     */
    long deleteByAccountId(String accountId);
}
