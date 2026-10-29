/**
 * 领域实体层：JPA 映射对象 + 值对象 + 枚举。
 *
 * <p>按业务域分子包，一个子包就是一个<strong>聚合</strong>：
 *
 * <ul>
 *   <li>{@code market} —— 标的（也是 K 线 CSV 目录的锚）</li>
 *   <li>{@code propfirm} —— PF 模板 + PF 账户 + 规则（<strong>账户是全库唯一的操作归属点</strong>）</li>
 *   <li>{@code indicator} —— 指标定义</li>
 *   <li>{@code drawing} —— 绘图图形 + 它的样式与锚点</li>
 *   <li>{@code trade} —— 挂单、成交，以及不落库的持仓/逐批归因值对象</li>
 *   <li>{@code voice} —— 语音讲解的元数据</li>
 *   <li>{@code audit} —— 操作记录（append-only）</li>
 *   <li>{@code strategy} —— 策略注册表 + 机器人运行摘要</li>
 *   <li>{@code record} —— 交易记录（一笔完整交易的过程 + 摘要）</li>
 * </ul>
 *
 * <p>四条硬规矩：
 *
 * <ol>
 *   <li><strong>跨聚合只按 id 引用，不写 {@code @ManyToOne}。</strong> 聚合之间通过
 *       {@code xxxId} 字段关联，外键由 DDL 上的 {@code REFERENCES} 让数据库守。
 *       这样加载一个绘图不需要顺带把账户、标的都拉起来，也不会踩 LazyInitialization。</li>
 *   <li><strong>账户只是统计点，不是容器。</strong> 图形、挂单、成交、语音、操作记录、
 *       机器人、交易记录、指标都用 {@code accountId} 指向 {@code PropFirmAccount}，
 *       但删账户<strong>只清空归属、不删数据</strong>（外键 {@code ON DELETE SET NULL}）。
 *       所以这些 {@code accountId} 列必须可空——可空的是「账户被删后的历史残留」，
 *       不是「新建时可以不给账户」：后者由各实体的工厂方法当场拦掉。</li>
 *   <li><strong>能做的不算，算的不存。</strong> 持仓、已实现盈亏、指标值、斐波那契线位置
 *       全部是派生的，一律现算（{@code service.TradeLedger}）。存副本迟早和事实对不上，
 *       而对不上的时候没有任何办法判断谁对。</li>
 *   <li><strong>金额一律 BigDecimal。</strong> 对应列是 TEXT（SQLite 没有 decimal），
 *       由 {@code converter.BigDecimalTextConverter} 在边界上转换。</li>
 * </ol>
 */
package com.tradingviewj.server.entity;
