package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.market.Symbol;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.Optional;

/**
 * 标的仓库。
 *
 * <p>方法粒度是「一次读写」，不做业务判断 —— 业务判断在 service。
 */
public interface SymbolRepository extends JpaRepository<Symbol, String> {

    /** K 线按 folder 找目录，抓取侧按 folder 写文件，所以这是最常用的一个查询。 */
    Optional<Symbol> findByFolder(String folder);
}
