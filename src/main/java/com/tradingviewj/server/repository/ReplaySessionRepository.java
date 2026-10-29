package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.session.ReplaySession;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface ReplaySessionRepository extends JpaRepository<ReplaySession, String> {

    List<ReplaySession> findBySymbolId(String symbolId);
}
