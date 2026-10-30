# 交易引擎：事件驱动架构实现方案

> 范围：挂单、撮合成交、平仓三条主线的**实现方案**，不写代码。
> 所有约束都对齐现有代码（`Order` / `Trade` / `TradeLedger` / `PropFirmSession` / `AuditEvent` / `schema.sql`）。
> 标 **[已核对]** 的是读过代码确认的；标 **[建议]** 的是需要你拍板的；标 **[推测]** 的是没实测的。

---

## 〇、先纠正三件事，否则后面全是错的

### 1. 事件驱动 ≠ 事件溯源

词汇表里这两个词离得很近，工程后果差一个数量级。

| | 事件溯源（Event Sourcing） | 本方案：事件驱动（Event-Driven） |
|---|---|---|
| 状态存在哪 | 事件流里，当前状态是重放算出来的 | **表里**（`trade_order.status` / `trade` 行） |
| 事件的角色 | 唯一真相 | **变更通知**，可丢、可重投 |
| 丢一条事件会怎样 | 账永久错 | 某个投影落后，重建即可 |
| 能不能直接 SQL 查「我有哪些挂单」 | 不能，得重放 | 能，`WHERE status='OPEN'` |

`AuditEvent` 的类注释已经把这条写死了 **[已核对 `entity/audit/AuditEvent.java:20-24`]**：

> 它**不是**业务状态的来源 —— 图形、挂单、成交各自有自己的表。这条分界不能破。

所以本方案**不做**：从 `audit_event` 重放挂单和持仓、把 `trade` 表降级成投影、给事件加版本号做 schema 演进。

### 2. 不要上消息中间件

单机、单进程、SQLite、单写者、5 分钟 K 线。引入 Kafka/RabbitMQ 的代价是**把一笔成交从"一个本地事务"变成"本地事务 + 一次跨进程投递"**，然后你必须为这半步补一张 outbox 表、一个投递重试器、一个消费位点表 —— 换来的是你根本用不上的吞吐。

用一个判据：

> **消费者是否在另一个 JVM / 另一台机器？**
> 否 → 进程内事件（`ApplicationEventPublisher`）。
> 是 → 才谈 outbox 或 broker。

当前所有消费者（考核会话、交易记录投影、界面状态）都在同一个进程里。

### 3. 不是所有环节都该事件化 —— 这是本方案最重要的判断

初学者最容易犯的错是"既然叫事件驱动，那就把撮合也做成事件"。**错。** 撮合有严格的先后依赖：

```
撮合 → 产生成交 → 重放账本 → 判权益 → 可能 FAILED → 未成交挂单必须作废
```

如果这四步是四个事件，事件之间没有顺序保证，"判权益"可能早于"成交落库"，算出来的是上一根的数。而且用户在这一根 `advance` 的 HTTP 响应里就要看到"距爆仓还有 \$482" —— 异步拿不到。

给一条能记住的判据：

> **这个动作失败，事实表该不该回滚？**
> 该 → **编排**（同步方法调用，在一个事务里，有返回值，有顺序）
> 不该 → **通知**（`AFTER_COMMIT` 事件，无顺序，失败只记日志）

按这条判据切一刀，得到本方案的核心分工表：

| 环节 | 归类 | 理由 |
|---|---|---|
| 撮合、生成成交 | **编排** | 失败必须回滚订单状态，不能出现"单变 FILLED 但没有 trade 行" |
| 成交流水号 `seq` 分配 | **编排** | 同一事务内取 `max(seq)+1`，出去了就没法保证 |
| 考核判定（余额/爆仓/升级） | **编排** | 结果要同步返回；FAILED 要在同一事务里把订单作废 |
| 审计流水 `audit_event` | **编排** | 它承载 `TradeRecord.startSeq/endSeq` 的区间语义，写失败必须回滚 |
| 交易记录摘要（MFE/MAE/标签） | **通知** | 晚 100ms 无所谓，失败可重建 |
| 界面状态推送 / 缓存放行 | **通知** | 纯展示 |
| 阶段统计落盘（`prop_firm_stage_log` 的汇总列） | **通知** | 可从 `audit_event` + `trade` 重算 |

**[建议]** 这张表就是方案的第一性约束。后面所有设计都是它的推论。

---

## 一、架构分层与模块划分

### 1.1 逻辑分层（职责，不是包结构）

```
┌─ api ──────────────────────────────────────────────────────┐
│ Controller：协议转换、参数校验、R<T> 外壳。不含任何业务判断  │
└────────────────────────┬───────────────────────────────────┘
                         │ 同步调用
┌─ application（用例编排）▼───────────────────────────────────┐
│ TradingService / ReplayService                              │
│ · 事务边界（一个 public 方法 = 一个事务）                    │
│ · 加锁（按 accountId 串行化）                               │
│ · 编排顺序：撮合 → 落账 → 考核 → 写审计 → 发事件             │
└──────┬───────────────────────────────┬─────────────────────┘
       │ 纯函数调用                     │ publishEvent
┌─ domain（纯 Java，无 Spring）▼──┐  ┌─▼─ projection（事件订阅）──┐
│ Matcher        撮合规则          │  │ TradeRecordProjector       │
│ TradeLedger    账本重放          │  │ SessionCacheEvictor        │
│ PropFirmSession 考核判定         │  │ StageSummaryProjector      │
│ Order/Trade    状态机与不变量    │  │ 只写「可重建」的东西        │
└──────┬───────────────────────────┘  └────────────────────────────┘
       │
┌─ infrastructure ─────────────────────────────────────────────┐
│ repository（JPA / SQLite）· storage（CSV）· event（发布器）    │
└──────────────────────────────────────────────────────────────┘
```

三条硬规矩：

1. **domain 层不许出现 `@Service` / `@Transactional` / `@Autowired`**。`TradeLedger` 已经是这个风格 **[已核对 `service/TradeLedger.java:34-41`：`final class` + private 构造]**，`Matcher` 照抄。好处是撮合的 40 个边界用例能在毫秒级跑完，不启 Spring、不建库、不读文件。
2. **application 层是唯一的事务边界**。`@Transactional` 只出现在这里，别下放到 service 内部的辅助方法上。
3. **projection 层禁止写事实表**。它只能写"删掉后能重算回来"的东西。这条是防止「事件回滚了但副作用还在」的兜底。

### 1.2 物理位置（尽量少动现有包）

现有 `com.tradingviewj.server` 是 `controller / service / repository / entity / storage / dto / enums / config / web` **[已核对，106 个 java 文件]**。**[建议]** 不重排包，只增量：

| 新增 | 位置 | 内容 |
|---|---|---|
| `service/Matcher.java` | 与 `TradeLedger` 同级 | 撮合纯函数 |
| `service/TradingService.java` | `service` | 下单/撤单/推进/收摊的用例入口 |
| `service/AccountLocks.java` | `service` | 按 accountId 的锁表 |
| `service/BarStream.java` | `service` | 跨日文件串联的 K 线迭代器 |
| `service/SessionRegistry.java` | `service` | `PropFirmSession` 常驻缓存 |
| `event/`（新包） | 顶层 | 事件定义 + 发布器 |
| `event/projection/` | 顶层 | 监听器 |
| `entity/trade/CancelReason.java` | `entity/trade` | 撤单原因枚举 |

**为什么不学 DDD 建 `domain/` 顶层包**：迁移 106 个文件不产生任何行为收益，且 `entity/trade` 已经在扮演领域模型的角色。分层是**职责约束**，不是目录结构。

---

## 二、事件模型

### 2.1 三类东西，不要混

| 类型 | 命名 | 调用方式 | 有返回值 | 可能失败 | 落库 |
|---|---|---|---|---|---|
| 命令 Command | `PlaceOrderCommand` | 同步 | 有 | 会（异常） | 否 |
| 领域事件 Domain Event | `OrderFilled` | 同步发布 / 异步消费 | 无 | 不该（吞掉记日志） | 否 |
| 审计事件 Audit Event | `AuditEvent` 实体 | 随事务写库 | 无 | 会（回滚） | **是** |

**「失败」不定义事件。** 下单被拒（账户已 FAILED、超手数上限）就是抛 `BusinessException` / 返回 `R.fail(code)`，它不是一个"发生过的事实" —— 什么都没发生。只有"已经提交、再被后续规则作废"才需要事件，例如：

- `OrderCancelled(reason=OCO_MUTEX)`：同一根 K 线里另一张单先成交了
- `OrderCancelled(reason=CLOSE_OUT)`：23:55 强平
- `OrderCancelled(reason=ACCOUNT_FAILED)`：账户爆仓，剩余挂单全部作废

### 2.2 事件清单

```java
sealed interface TradeEvent permits OrderPlaced, OrderFilled, OrderCancelled,
                                BarAdvanced, AccountFailed, AccountPromoted
```

| 事件 | 关键字段 | 发布时机 |
|---|---|---|
| `OrderPlaced` | orderId, accountId, symbolId, side, type, price, qty, placedAtBar, ocoGroup | 挂单落库后 |
| `OrderFilled` | orderId, tradeId, seq, barTime, fillPrice, qty, realizedPnl, commission | 成交落库后（同一根可能多条） |
| `OrderCancelled` | orderId, reason, barTime | 撤单落库后 |
| `BarAdvanced` | accountId, symbolId, prevBarTime, barTime | 游标推进后（**最后发**） |
| `AccountFailed` | accountId, symbolId, barTime, equity, floorLine, message | 判权益触线后 |
| `AccountPromoted` | accountId, symbolId, barTime, fromStage, toStage | 升级后 |

**没有** `PositionOpened` / `PositionClosed` / `BalanceChanged`。持仓和余额是 `Trade` 的重放结果（`TradeLedger`），独立定义事件等于给它们开第二条真值来源 —— 现有代码已经明确否掉这种做法 **[已核对 `service/TradeLedger.java:18-22` 与 `SERVER-DESIGN.md` §五.2]**。

### 2.3 每个事件必须带三样

```java
record EventHeader(
    String eventId,        // UUID，消费幂等键
    Instant occurredAt,    // 真实时钟：这条事件何时产生
    String barTime         // 行情时钟：会计入哪根 K 线
)
```

两个时钟必须分开。这是项目已经付过学费的教训 —— `prop_firm_account.started_at`（真实时钟）与 `start_bar_time`（行情时钟）就是同一对概念 **[已核对 `schema.sql:103-115`]**。事件里把两者合并成一个 `timestamp`，半年后没人分得清"这根 K 线是 2024-03-04 09:00 的，还是 2026-10-30 才被复盘的"。

### 2.4 事件与 `audit_event` 是两条流，不要合并

| | 领域事件 | `audit_event` |
|---|---|---|
| 视角 | 系统：发生了什么 | 用户：这一步在盘面上长什么样 |
| 是否落库 | 否 | 是，append-only |
| 消费者 | 投影、缓存 | 回放、交易记录区间 |
| 保留策略 | 内存，进程重启即无 | 永久，不能按时间窗删 |
| 载荷 | 强类型 record | JSON 字符串 |

`ReplayAction` 里已经有 `ORDER_PLACED / ORDER_FILLED / ORDER_CANCELLED / ORDER_MOVED` **[已核对 `enums/ReplayAction.java:46-55`]**，所以同一件事会写两份：**事务内写 `audit_event`（用户视角），提交后发领域事件（系统视角）**。

**[建议]** 明确不许"让投影去消费 `audit_event`"。原因：那要求把 `payload_json` 反序列化回来，既慢又脆，而且 `audit_event` 的保留策略（永久）和事件消费（一次性）根本不是一回事。

---

## 三、交易环节的事件流转

### 3.1 推进一根 K 线（心脏）

这是唯一会产生"非用户主动成交"的路径。

```
POST /api/trading/advance {accountId, symbolId, expectedBarTime}
  │
  ├─ [0] requireTradable(account)              ← 账户闸门，失败直接 400
  ├─ [1] lock(accountId)                       ← 单账户串行
  ├─ [2] cursor = 最后一条 audit_event.barTime  ← 进度不单独存
  │      if (expectedBarTime != cursor) → 409   ← 幂等保护
  ├─ [3] bar = bars.next(cursor)                ← 越界就换日文件，再没有就结束
  ├─ [4] if (bar 落在 04:45–06:00) → 返回 SKIPPED，不撮合、不动游标【见 §7.6】
  │
  ├─ [5] fills = Matcher.match(openOrders, bar, symbol)   ← 纯函数
  ├─ [6] for each fill:
  │         order.markFilled(bar.barTime, fill.price)     ← 状态机
  │         trade = Trade.fill(..., seq = max(seq)+1, commission = symbol.commission)
  │         tradeRepository.save(trade)
  ├─ [7] OCO 互斥：fills 覆盖不到的组员 → order.cancel() + OrderCancelled(OCO_MUTEX)
  ├─ [8] step = session.of(accountId).onBar(symbolId, bar) ← 编排，不是事件
  ├─ [9] account.syncComputed(step.balance(), step.eodPeak())  ← 只许追平，不许增量
  ├─ [10] if (step.event == FAILED):
  │          account.fail(...) + 该品种所有 OPEN 单 → CANCELLED(ACCOUNT_FAILED)
  │      if (step.event == PROMOTED):
  │          account.promote(...) + sessionRegistry.evict(accountId)
  ├─ [11] auditEventRepository.save(BAR_ADVANCED, barTime=bar.barTime, seq=next)
  │       （有成交时先写 ORDER_FILLED 若干条，再写 BAR_ADVANCED）
  └─ [12] commit → 发事件：OrderFilled×N → OrderCancelled×M → BarAdvanced
```

**为什么第 12 步在 commit 之后**：`@TransactionalEventListener(phase = AFTER_COMMIT)`。如果改成事务内 `publishEvent` + `@EventListener`，投影抛异常会把**已经算好的成交一起回滚**，而失败的却是一个只写展示数据的投影 —— 这就是把"次要故障"升级成"主流程故障"。

**为什么第 8 步不是事件**：`step` 的返回值要进第 9 步和第 10 步，而第 10 步可能让订单作废 —— 有顺序依赖、失败要回滚，按 §0.3 判据属于编排。

### 3.2 下单（挂单 / 市价）

```
POST /api/trading/orders
  ├─ requireTradable(account)
  ├─ qty 必须是正整数；price 必须 > 0（LIMIT/STOP）
  ├─ 手数上限：PropFirmPositionLimit.maxContracts(account, stage, symbol)
  │            按「净持仓 + 未成交挂单」计算【见 §7.2】
  ├─ effectiveFromBar = 下一根  ← 本根挂的本根不撮合
  ├─ order = Order.place(...)   ← 一律 OPEN，市价单也不例外
  ├─ if (type == MARKET):
  │      order.markFilled(currentBar.barTime, currentBar.close)
  │      tradeRepository.save(Trade.fill(..., commission))
  │      session.onBar(symbolId, currentBar)   ← 同一根重算一次
  ├─ auditEvent.save(ORDER_PLACED [ORDER_FILLED])
  └─ commit → OrderPlaced(+OrderFilled)
```

市价单走同一条状态机，是现有代码已经修过的坑 **[已核对 `Order.java:130-133` 的注释]**，别退回去。

### 3.3 平仓（三种入口，一个出口）

| 入口 | 触发者 | 实现 |
|---|---|---|
| 主动平仓 | 用户点"平仓" | 反向 **MARKET** 单，`qty ≤ |净持仓|` |
| 挂单平仓 | 价格触发 | 反向 **LIMIT/STOP** 单，被 `Matcher` 触发 |
| 系统强平 | 23:55 / 收摊 / 账户 FAILED | 反向 **MARKET** 单，服务端构造 |

**三种都收敛到同一件事：产生一笔反向 `Trade`。** 现有 `TradeLedger` 已经能把任意反向成交解释成平仓（`Trade.java` 与 `TradeLedger.java:90-128`）**[已核对]**，所以不新增"平仓"交易类型，也不加 `isClose` 标记 —— 加了就多一个可以和净持仓符号不一致的字段。

两个必须校验的点：

1. **`qty ≤ |净持仓|`**。平 3 手但只有 2 手，应该 **400 拒绝**，不许自动变成"平 2 手 + 开 1 手反向仓"。反手是另一种意图，必须由下单方向显式表达。
2. **平仓前重新读一次净持仓**，不能信任前端传来的持仓数。持仓是重放结果，不是存的状态。

**强平的顺序：先平后撤**（SERVER-DESIGN §四.4 已定）。倒过来一旦平仓失败就变成"挂单没了、仓还在"，那是用户最不想要的状态。

---

## 四、订单生命周期管理

### 4.1 状态机：三个状态，不再加

```
        ┌──────────┐
place() │   OPEN   │
   ────►│          │
        └──┬────┬──┘
  markFilled │    │ cancel()
            ▼    ▼
       FILLED  CANCELLED
            终态    终态
```

**[建议] 不加 `PENDING / SUBMITTED / PARTIALLY_FILLED / REJECTED`。** 理由：

- 没有券商、没有交易所、没有网络确认。`SUBMITTED` 是一个**没人能观测到**的状态，加了就是给同步调用编一段不存在的旅程。
- 部分成交明确不做（SERVER-DESIGN §3.1）。没有部分成交就不需要 `PARTIALLY_FILLED`。
- `REJECTED` 见 §2.1：拒绝是命令的返回值，不是订单的状态。订单从没存在过，哪来的状态。

### 4.2 但"为什么撤的"要有地方放

三个状态不够表达的信息，用**字段**而不是**状态**：

```java
enum CancelReason {
    USER,           // 用户撤单
    OCO_MUTEX,      // 同组另一张先成交，本根作废
    CLOSE_OUT,      // 23:55 强平前的清场
    ACCOUNT_FAILED, // 账户爆仓
    MANUAL_FLAT     // 用户点"全部平仓"
}
```

`trade_order` 加一列 `cancel_reason TEXT`（可空，仅 CANCELLED 时有值）。**状态数不涨，可解释性上升**。这是"用字段表达维度，别把维度做成状态"的常规做法 —— 状态一多，`switch` 会指数膨胀，而且每个新状态都要重新回答"它能不能成交"。

### 4.3 时序约束：本根挂的本根不撮合

需要一个**撮合生效时间**，且必须是 K 线时间明文（不是下标，项目已经全线清掉了下标字段 **[已核对 `schema.sql:262-268`]**）：

```
trade_order.effective_from_bar TEXT NOT NULL
  · 新挂单 = placedAtBar 的下一根（下单时算好写入）
  · 改价时更新为「改价那一根的下一根」
```

**为什么不直接用 `placedAtBar` 推**：改价（`ORDER_MOVED`）之后，这张单在时间上已经是"新单"，如果还按 `placedAtBar` 算，等于用改价前的那根 K 线去撮合改价后的价格。这是一条真实的漏账路径，不是洁癖。

`Matcher` 的第一行就是：

```java
orders.stream().filter(o -> o.effectiveFromBar().compareTo(bar.barTime()) <= 0)
```

### 4.4 并发：按账户串行，不用乐观锁

一张单可能被三个请求同时碰：撤单、撮合（advance）、强平。

**[建议] 用 `AccountLocks`（按 accountId 的 `ReentrantLock`），不用 `@Version` 乐观锁。** 理由：

> 撮合是"**读一批单 → 算 → 改一批单**"。乐观锁在冲突时会让第二个请求重试，而**重试时看到的订单集合已经变了**，两次计算的结果不一致，最终哪次生效取决于重试时机 —— 撮合变成了不确定的。

这条比吞吐重要得多。单机、一个用户、5 分钟一根 K 线，锁竞争约等于零。

锁的粒度：**账户**，不是品种。因为权益是账户级的（跨品种合并），两个品种同时推进会让 `session.onBar` 的调用顺序不确定，从而影响"同一时刻哪笔成交先应用"。

### 4.5 幂等推进

进度 = `该(账户,品种)最后一条 audit_event 的 bar_time` **[已核对 `SERVER.md` 记录与回放章节]**。这个设计省了一张表，但带来一个问题：**响应丢了、前端重试，会多推一根**。

修法很便宜：请求带 `expectedBarTime`，服务端比对游标。

```
if (!cursor.equals(req.expectedBarTime)) → 409 Conflict
    { code: "CURSOR_STALE", data: { current: cursor } }
```

前端拿到 409 就刷新状态，不重推。这是乐观并发的标准做法，比"请求去重表"轻。

---

## 五、撮合规则

### 5.1 签名

```java
public final class Matcher {
    public static List<Fill> match(List<Order> openOrders, Bar bar, BigDecimal tick);
}
```

纯函数：入参只有订单列表、当前 K 线、最小跳动。**不读库、不看账户、不算手续费。** 手续费在 `Trade.fill` 时快照，属于落账环节。

`Fill` 只回答"哪张单、成交在什么价、多少手"。

### 5.2 触发与定价（四象限，唯一一张表）

| 类型 | 方向 | 触发条件 | 成交价 | 例（挂 100） |
|---|---|---|---|---|
| LIMIT | BUY | `low ≤ p` | `min(p, open)` | open=98,low=97 → **98**（低开更优） |
| LIMIT | SELL | `high ≥ p` | `max(p, open)` | open=103,high=104 → **103** |
| STOP | BUY | `high ≥ p` | `max(p, open)` | open=106 → **106**（不白捡） |
| STOP | SELL | `low ≤ p` | `min(p, open)` | open=94 → **94** |

跳空修正是**不能省**的那条：不加的话每一次跳空成交都白送钱，回测数字全废。这在 `SERVER-DESIGN.md` §3.1 已经定了，这里只是把它变成一张可以逐行写成测试的表。

### 5.3 处理顺序（确定性）

```
1. 过滤：effectiveFromBar <= bar.barTime  且  status == OPEN
2. 稳定排序：按 (placedAtBar, orderId) 升序
3. 逐个判触发 → 收集候选
4. OCO 分组裁决：同组候选 > 1 时，保留优先级最高的一张
     优先级：STOP > LIMIT > MARKET（组内不会同时出现 MARKET）
     同类型多张：保留 orderId 字典序最小的一张（任意但是确定）
5. 未胜出的组员 → CancelOco
6. 输出 Fill 列表（按第 2 步的顺序）
```

**第 4 步为什么要定"止损优先"**：5 分钟 K 线只有 OHLC，分不出 high 和 low 谁先到。这时候有两条路：

- 选对持仓**有利**的那一侧（止盈先成交）→ 每次说不清的地方都替用户选好结果，考核数字偏乐观。
- 选对持仓**不利**的那一侧（止损先成交）→ 偏悲观。

选悲观。理由是**这套系统是用来训练和考核的，宽松的假设会让人在真实市场里第一次见到"止损先到"时毫无准备**。

**确定性是硬要求**：同一组输入必须输出完全相同的 `Fill` 列表。否则同一个复盘动作跑两遍得到两个账户余额，用户会立刻不信任这个系统。测试用 golden case 表钉住（见 §9 第一步）。

### 5.4 不做的部分

- 部分成交、滑点、盘口深度、成交量约束
- 盘前盘后
- 同一根 K 线上多次触发同一张单（一单只成交一次，`markFilled` 已经是领域的硬约束 **[已核对 `Order.java:145-152`]**）

---

## 六、数据一致性保障

### 6.1 五个不变量（写进测试，不是写进注释）

| # | 不变量 | 违反的表现 |
|---|---|---|
| I1 | `Σ trade.realizedPnl` = `TradeLedger.replay(全部成交).realizedPnl()` | 余额与流水对不上 |
| I2 | `order.status == FILLED` ⟺ 存在 `trade.order_id` 指向它 | 幽灵成交 / 幽灵挂单 |
| I3 | `trade.seq` 在同一 `account_id` 内从 1 连续无跳号 | 排序不确定，均价在反手时算错 |
| I4 | `account.balance == base_balance + Σ trade.netPnl()`（本阶段） | 余额被增量累加写脏 |
| I5 | 同一根 K 线上 `trade` 的顺序 = `Trade.executionOrder()` 顺序 | 逐批归因随读库顺序变 |

I1 和 I4 是同一件事的两面。**`applyNetPnl` 已经被删掉了**（SERVER-DESIGN §五.2），新增的事件投影绝不能再开一个"改余额"的入口。

### 6.2 事务边界

**一个 public 用例方法 = 一个事务。** 现在的 `advance` 里包含：订单状态变更 + 成交插入 + 游标（audit_event）+ 账户缓存同步 + 阶段日志。这五件事必须同生共死。

反面例子：把"更新账户余额"放到 `AFTER_COMMIT` 里。那样事务回滚了余额却变了，或者余额更新失败成交却已成立 —— 两种都是账本不可修复的错误。

CSV 被 Excel 占用 → 读不出来 → 整根回滚 → 返回可重试错误码。这是既有行为，别改。

### 6.3 `seq` 分配

```java
// 必须在同一个事务内，且在账户锁内
int next = tradeRepository.maxSeqByAccountId(accountId) + 1;
```

唯一的兜底是 `ux_trade_account_seq` 唯一索引 **[已核对 `schema.sql:295`]**：真出现并发写，它会抛 `ConstraintViolationException`，事务回滚 —— 宁可当场红，也不要写出一个第二笔 seq=7。

**[建议]** 不要去改成数据库自增序列。成交对象在落库前就要拿 `seq` 排序（`Trade.executionOrder()`），自增列拿不到值。

### 6.4 事件投递的可靠性：不追求，而是"丢了也没关系"

这是本方案最省事的一处设计。`AFTER_COMMIT` 事件是**内存投递**，进程在 commit 和 publish 之间崩溃 → 事件丢失。

不补 outbox，因为：

- 所有投影都能从 `trade` / `audit_event` 重建（`SessionRegistry.of()` 本来就是懒加载重建 **[参考 `SERVER-DESIGN.md` §六]**）；
- 丢事件的最坏后果是"交易记录的 MFE/MAE 暂时是空的"，下次 `finish()` 或重启时补上；
- 而如果哪天真的需要跨进程，那时候加 outbox 才是有理由的 —— 现在加是为了一个不存在的需求付出永久的复杂度。

**对应的硬约束**：投影必须幂等（按 `eventId` upsert），且必须能"全量重建"（启动时对未收尾的 `trade_record` 跑一遍）。两条都要有，否则"丢了没关系"就是一句空话。

### 6.5 事务提交后抛异常怎么办

```java
@TransactionalEventListener(phase = AFTER_COMMIT)
public void on(OrderFilled e) {
    try { ... } catch (RuntimeException ex) {
        log.error("投影失败，已标记待重建: {}", e.eventId(), ex);
        rebuildQueue.add(e.accountId());   // 只记，不抛
    }
}
```

**必须吞掉。** 事务已经提交了，往上抛只会让用户看到一个"失败"的响应，而实际上成交已经成立 —— 用户会重复下单。

---

## 七、异常与边界场景

按"用户能不能看懂"分级，不要用一个 `RuntimeException` 包打天下。

### 7.1 命令被拒（同步返回，不产生任何状态）

| 场景 | 处理 |
|---|---|
| 账户 ACTIVE？ | `requireTradable` 抛 `BusinessException` → 400 |
| 阶段是 PAYOUT？ | 同上（`stage == PAYOUT` 不能再下单，但账户仍 ACTIVE） |
| 手数 ≤ 0 / 非整数 | 400，实体工厂方法拦（`BigDecimal` 落库，`scale() > 0` 就拒） |
| 价格 ≤ 0 | 400 |
| 超手数上限 | 400，带"当前上限 N 手 / 阶段 FUNDED" |
| 游标还没推进过 | 400 "先推进一根再下单" |
| 平仓量 > 净持仓 | 400 "持仓只有 2 手" |

### 7.2 手数上限算"净持仓 + 未成交挂单"

这是容易被忽略的一条：如果只算净持仓，用户可以挂 20 张 20 手的限价单，全在下一根触发 —— 相当于瞬间 400 手。**上限必须把 OPEN 状态的挂单算进去。**

`PropFirmPositionLimit` 现在是按 `stage` 分叉（FUNDED 分档，EVAL 固定 40）**[已核对 `SERVER-DESIGN.md` §3.3 与 `SERVER.md`]**。改法是加一个"待成交手数"入参，判 `|净持仓| + Σ|OPEN 单 qty| ≤ 上限`。

### 7.3 边界场景清单

| # | 场景 | 规则 |
|---|---|---|
| B1 | 同一根 K 线里止损和止盈都可能触发 | OCO 裁决，止损优先 |
| B2 | 反手单：一笔成交同时平旧仓 + 开新仓 | `TradeLedger` 已支持；但**下单接口不许用溢出数量隐式反手**（§3.3） |
| B3 | 权益 == 亏损线 | **判 FAILED**（等于即失败，用户明确要求） |
| B4 | 账户已 FAILED 还被推进 | `terminal()` 直接返回，不撮合、不产生成交；剩余 OPEN 单已在失败那一刻作废 |
| B5 | 23:55 那根同时有挂单触发 | **先撮合、后强平**。因为 23:55 的强平是"收盘动作"，价格已经走完了；反过来的话，同一根里会用收盘价把本该触发的挂单抹掉 |
| B6 | 04:45–06:00 的 12 根 | **不撮合、不动游标、返回 SKIPPED**。挂单保持 OPEN，但不会被这些 K 线触发 |
| B7 | 跨日文件（当天推完） | 换下一个 CSV；没有下一个 → 返回 `END_OF_DATA`，前端提示"要收尾请调 finish" |
| B8 | 用户停在最后一根没调 `finish` | 最后一个交易日不结算、峰值不更新、EVAL 不升级。前端"退出"必须调 `finish` **[`SERVER-DESIGN.md` §四.3 已强调]** |
| B9 | 重复推进同一根 | 409 `CURSOR_STALE`（§4.5） |
| B10 | 两个品种并发推进 | 账户锁串行；权益跨品种合并计算 |
| B11 | CSV 被 Excel 占用 | 整根回滚，返回 `BAR_STORE_BUSY`，可重试 |
| B12 | CSV 有坏行 | `CsvBarStore` 已 warn 并跳过 **[已核对 `storage/CsvBarStore.java:93-97`]**；坏行导致缺口不报警（这是已知取舍，因为时间列是明文，越界自检恒为 0） |
| B13 | 挂单价格远离市场（永远不触发） | 不设过期；由 23:55 强平统一清场 |
| B14 | 一次 `advance` 触发多张单（含 OCO 多组） | 全部在同一事务、同一根；`seq` 按 `Matcher` 输出顺序连续分配 |
| B15 | 进程崩溃在事务中间 | SQLite 回滚；重启后游标仍在上一根，重推即可 |
| B16 | 投影失败 | 吞异常 + 标记待重建（§6.5） |
| B17 | 账户被删 | 挂单/成交/事件全部保留 `account_id`（不加外键），`TradeRecord` 靠 `account_name` 快照仍可读 |

### 7.4 一个容易漏的：交易记录的开/收

规则：**空仓 → 有仓 开始，回到空仓 收尾**。

- 起点锚定在"开仓成交**之前**那一根"（`BASE` 事件），因为记录的第一屏应该是"还没进场"的样子。
- 一根 K 线内开了又平 → 也是一条完整记录。
- MFE/MAE 在收尾时全量重放，**排除有成交的那几根**（一根只有 OHLC，有成交那根的高低点等于替交易者挑时机）。

这条属于 §0.3 里的"通知"（晚一点没关系），所以放在 `TradeRecordProjector` 里，不阻塞 `advance` 的响应。

---

## 八、关键接口

沿用现有 `R<T>` 外壳和 `GlobalExceptionHandler` **[已核对 `web/GlobalResponseAdvice.java`、`web/GlobalExceptionHandler.java` 存在]**。

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/trading/orders` | 挂单（LIMIT / STOP） |
| `POST` | `/api/trading/orders/market` | 市价单（含平仓） |
| `POST` | `/api/trading/orders/{id}/cancel` | 撤单 |
| `PUT` | `/api/trading/orders/{id}` | 改价（**建议：一期不做**，见 §10） |
| `GET` | `/api/trading/orders?accountId&symbolId&status` | 挂单列表 |
| `POST` | `/api/trading/advance` | 推进一根，**唯一产生被动成交的入口** |
| `POST` | `/api/trading/finish` | 收尾结算（用户停在最后一根时调） |
| `POST` | `/api/trading/flat` | 全部平仓 + 撤单（先平后撤） |
| `GET` | `/api/trading/state?accountId&symbolId` | 盘面快照：持仓/挂单/权益/距爆仓 |

### 8.1 `advance` 的响应形状

```json
{
  "code": 0,
  "data": {
    "event": "RUNNING | SKIPPED | FAILED | PROMOTED | END_OF_DATA",
    "barTime": "2024-03-04 09:35",
    "fills": [
      {"orderId": "...", "side": "BUY", "price": "5210.25", "qty": 2,
       "seq": 17, "realizedPnl": "0", "commission": "1.00"}
    ],
    "cancelled": [{"orderId": "...", "reason": "OCO_MUTEX"}],
    "position": {"quantity": 2, "avgPrice": "5210.25"},
    "account": {"balance": "49999.00", "equity": "50012.50",
                "floor": "49700.00", "distanceToFloor": "312.50"}
  }
}
```

`distanceToFloor` 是界面上"距爆仓还有 \$482"那个数。它必须由后端算 —— 前端自己拿 `equity - floor` 算，就等于在客户端复制了一套账务逻辑。

### 8.2 服务层签名（给你看职责边界，不是最终代码）

```java
@Service
public class TradingService {
    public Order placeOrder(PlaceOrderCommand cmd);
    public Order marketOrder(MarketOrderCommand cmd);
    public void cancel(String accountId, String orderId);
    public AdvanceResult advance(String accountId, String symbolId, String expectedBarTime);
    public Step finish(String accountId, String symbolId);
    public FlatResult flat(String accountId, String symbolId);
}
```

六个方法，全部 `@Transactional`，全部第一行 `AccountLocks.lock(accountId)`。**多一个都不加** —— 每加一个都会成为绕开锁和事务的入口。

---

## 九、落地顺序

原则：**每一步结束时系统都能跑，并且能证明它对。** 不要先铺 Controller。

### 第一步：撮合纯函数（不接数据库）

`Matcher` + `Fill` + 40 个手工构造 `Bar` 的用例。

**验收**（对着 §5.2 每一行 + §5.3 每一条）：
- 限价买挂 100，`low=99` → 100
- 同挂 100，`open=98` → 98
- 止损买挂 100，`open=106` → 106
- OCO 组内止损挂在限价之后 → 仍判止损成交
- `effectiveFromBar` 等于本根 → 不参与
- 同一输入跑两遍，`Fill` 列表逐字段相等

**这一步不引入任何事件。** 撮合正确性和事件投递是两件事，混在一起查不清。

### 第二步：绑上账本（`TradeLedger` 已有，只做串联）

`TradingService.placeOrder / marketOrder` + `seq` 分配 + `trade` 落库 + `TradeLedgerConsistencyTest`（I1）。

**验收**：MES 买 1 手 → `commission` = 标的费率，余额少一个单边；平掉后净盈亏 = `(平−开)×5×1 − 2×费率`。

### 第三步：接上考核（编排，不是事件）

`BarStream` + 游标 + `SessionRegistry` + `advance` + `finish`。

**验收**：推完 276 根全 `RUNNING`；把 `max_drawdown` 改极小 → 某根 `FAILED` 且 `account.status=FAILED`；`finish()` 后盈利日正确。

### 第四步：引入事件层（只加投影，不改主流程）

`event/` 包 + `@TransactionalEventListener` + `TradeRecordProjector`。

**验收**：
- 关掉所有监听器，`advance` 的行为和数据**一字不差** ← 这条是最重要的验收，证明事件层没有侵入主流程
- 打开监听器，一根 K 线内开平 → 生成一条完整 `trade_record`
- 监听器里强制抛异常 → `advance` 仍然返回成功，响应里的成交不变

### 第五步：多品种 + 跨日 + 收尾

`flat`（先平后撤）+ 23:55 / 04:50 两个收尾点 + 禁交易窗。

**验收**：两品种交替推进、同刻一多一空 → 权益各用自己最不利价；推过 04:45–06:00 → 全部 SKIPPED；`flat` 时把平仓改成必然失败 → 挂单一张不动。

### 第六步：改价（**建议一期不做**）

理由：改价引入 `effectiveFromBar` 的第二次更新、`ORDER_MOVED` 的审计语义、"改价后手数上限要不要重算"。收益是少点一次撤单+重下。等真有用户抱怨再说。

---

## 十、要你拍板的四件事

1. **改价支不支持**（§9 第六步）。建议一期不做。
2. **重试幂等用 `expectedBarTime` 还是请求去重表**。建议前者，一行判断，无新增表。
3. **同一账户多品种能不能并发推进**。建议不能，账户锁串行 —— 权益是跨品种合并的，并发会让"同一时刻谁先应用"不确定。
4. **事件要不要推给前端**。建议不要。现在 `advance` 是同步返回完整结果，事件的第一批消费者全在后端。真需要实时刷新再上 SSE，那时事件层的形状不用改。

---

## 附：和现有文档的关系

| 文档 | 分工 |
|---|---|
| `SERVER-DESIGN.md` | 业务规则（撮合四象限、考核、三套时间口径）—— **本文不改它，只引用** |
| `SERVER.md` | 当前实现的横切约定（BigDecimal、seq、日志三层） |
| 本文 | **交易机制的分层、事件边界、一致性与异常处理** |

§5.2 的四象限、§7.3 的 B3/B5/B6/B8 都直接来自 `SERVER-DESIGN.md` §3.1 与 §四，本文只是把它们形式化成可测的判据，没有新增业务规则。
