package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.market.CommissionSchedule;
import org.springframework.data.jpa.repository.JpaRepository;

/**
 * 手续费价目表仓库。
 *
 * <p>只有一行（{@link CommissionSchedule#SINGLETON_ID}），所以不需要任何派生查询 ——
 * 用 {@code findById(CommissionSchedule.SINGLETON_ID)} 就够了。加了 {@code findAll()}
 * 之类的便利方法反而会让人以为「可以有多份价目表」。
 */
public interface CommissionScheduleRepository extends JpaRepository<CommissionSchedule, String> {
}
