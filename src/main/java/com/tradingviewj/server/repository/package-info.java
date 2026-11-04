/**
 * 数据库读写层：每个聚合根一个 Spring Data JPA 接口。
 *
 * <p>方法粒度是「一次读写」——查一条、查一批、插一条、按条件更新，
 * <strong>不写业务判断</strong>；业务判断在 {@code service}。
 *
 * <p>依赖方向固定为 controller → service → repository → 数据库，repository 只允许 import
 * {@code entity} 和 JDK，不允许反过来 import {@code service} / {@code controller}，
 * 也不允许注入 Service，这是防循环依赖的第一道闸。
 *
 * <p>两条本项目的约定：
 *
 * <ul>
 *   <li><strong>查成交流水必须带排序</strong>（见 {@link com.tradingviewj.server.repository.TradeRepository}）。
 *       持仓和盈亏是按时间重放算出来的，顺序错了结果就错，而且是「看着像对的」那种错。</li>
 *   <li><strong>能用方法名派生查询就别写 {@code @Query}。</strong> 真需要 join 时再上 JPQL，
 *       不要提前为性能写一堆没人读得懂的 SQL。</li>
 * </ul>
 */
package com.tradingviewj.server.repository;
