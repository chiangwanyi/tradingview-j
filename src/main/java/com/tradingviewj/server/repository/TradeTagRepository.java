package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeTag;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

/**
 * 标签字典（{@code trade_tag}）。
 *
 * <p><strong>没有「按记录查标签」的方法</strong>：那件事走 {@code TradeRecord.tags}
 * （{@code @ManyToMany} 直接带出来），不从这里绕一圈。这个仓库管的是「标签本身」的生命周期：
 * 我有哪些标签、改名、换色、删掉一个没人用的标签。
 *
 * <p><strong>没有 {@code deleteByXxx} 的批量写法</strong>：标签被记录引用时不允许删
 * （{@code ON DELETE RESTRICT}），批量删只会撞上约束然后整批失败 ——
 * 要删就一个一个来，让「哪个标签还被用着」这件事在报错里暴露出来。
 */
public interface TradeTagRepository extends JpaRepository<TradeTag, String> {

    /** 全部标签，按名字排 —— 标签本子是个下拉/筛选面板，顺序必须是稳定的。 */
    List<TradeTag> findAllByOrderByNameAsc();

    /** 按名字找。名字上有唯一索引 {@code ux_tag_name}，所以最多一条。 */
    Optional<TradeTag> findByName(String name);
}
