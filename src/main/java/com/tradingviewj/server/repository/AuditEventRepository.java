package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.audit.AuditEvent;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

public interface AuditEventRepository extends JpaRepository<AuditEvent, String> {

    List<AuditEvent> findByAccountIdOrderBySeqAsc(String accountId);

    /**
     * 账户里最后一条操作。
     *
     * <p>下一条的 seq 就是它的 seq + 1。用「查最大再 +1」而不是数据库自增：
     * seq 是<strong>账户内</strong>的连续编号，跨账户不共用，全局自增做不到这件事。
     */
    Optional<AuditEvent> findTopByAccountIdOrderBySeqDesc(String accountId);
}
