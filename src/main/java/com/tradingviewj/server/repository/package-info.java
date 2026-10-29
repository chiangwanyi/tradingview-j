/**
 * 数据库读写层（预留，当前无实现）。
 *
 * <p>规划位置：每个数据表/集合一个接口，方法粒度是「一次读写」——查一条、查一批、插一条、按条件更新，
 * 不写业务判断；业务判断在 {@code service}。
 *
 * <p>依赖方向固定为 controller → service → repository → 数据库，repository 只允许 import
 * {@code entity}（表映射对象）和 JDK，不允许反过来 import {@code service} / {@code controller}，
 * 也不允许注入 Service，这是防循环依赖的第一道闸。
 *
 * <p>接口名先定下来，选型（MyBatis mapper / Spring Data JPA）之后再落实现；
 * 若最终选 MyBatis 且项目习惯叫 mapper，整个包改名即可，调用方只认方法签名。
 */
package com.tradingviewj.server.repository;
