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
 * <p>五条硬规矩：
 *
 * <ol>
 *   <li><strong>跨聚合只按 id 引用，不写 {@code @ManyToOne}。</strong> 聚合之间通过
 *       {@code xxxId} 字段关联，外键由 DDL 上的 {@code REFERENCES} 让数据库守。
 *       这样加载一个绘图不需要顺带把账户、标的都拉起来，也不会踩 LazyInitialization。</li>
 *   <li><strong>账户只是统计点，不是容器。</strong> 图形、挂单、成交、语音、操作记录、
 *       机器人、交易记录、指标都用 {@code accountId} 指向 {@code PropFirmAccount}，
 *       但删账户<strong>只删那一行、不动任何子数据</strong>。这些 {@code accountId} 列是
 *       <strong>NOT NULL 且不加外键</strong>：账户删掉后保留原来那个 id（指向一个已不存在的
 *       账户），既不置空也不级联删除。「新建时必须给账户」由各实体的工厂方法当场拦掉。</li>
 *   <li><strong>能做的不算，算的不存。</strong> 持仓、已实现盈亏、指标值、斐波那契线位置
 *       全部是派生的，一律现算（{@code service.TradeLedger}）。存副本迟早和事实对不上，
 *       而对不上的时候没有任何办法判断谁对。</li>
 *   <li><strong>金额一律 BigDecimal。</strong> 对应列是 TEXT（SQLite 没有 decimal），
 *       由 {@link com.tradingviewj.server.converter.BigDecimalTextConverter} 在边界上转换。</li>
 *   <li><strong>枚举字段存 Java 常量名。</strong> 用 {@code @Enumerated(EnumType.STRING)}，
 *       不再配 {@code AttributeConverter} —— 库里的值就是枚举名的意思，协议上的小写串
 *       由输出侧换算。转换器只在「Java 类型落不进 SQLite」时才存在。</li>
 * </ol>
 */
package com.tradingviewj.server.entity;
