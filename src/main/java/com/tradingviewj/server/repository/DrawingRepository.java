package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.drawing.Drawing;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/**
 * 图形按<strong>标的</strong>取，不按账户 —— 图形不挂在账户上，它挂在 K 线上。
 * 复盘时要的是「这个品种这张图上的所有线」，账户在那一刻无关。
 */
public interface DrawingRepository extends JpaRepository<Drawing, String> {

    List<Drawing> findBySymbolId(String symbolId);

    long countBySymbolId(String symbolId);
}
