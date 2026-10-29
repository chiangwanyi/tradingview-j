package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.drawing.Drawing;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface DrawingRepository extends JpaRepository<Drawing, String> {

    List<Drawing> findByAccountId(String accountId);

    long countByAccountId(String accountId);
}
