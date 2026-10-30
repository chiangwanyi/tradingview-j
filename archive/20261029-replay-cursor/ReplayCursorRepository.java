package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.replay.ReplayCursor;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.Optional;

/**
 * 复盘进度仓库。一个（账户, 品种）只有一行 —— 唯一索引在 schema.sql 里守着，
 * 这里不写「有就更新、没有就插入」的逻辑，那是 service 的事。
 */
public interface ReplayCursorRepository extends JpaRepository<ReplayCursor, String> {

    Optional<ReplayCursor> findByAccountIdAndSymbolId(String accountId, String symbolId);
}
