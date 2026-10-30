# 复盘系统：业务实现方案

> 这份文档只说三件事：**要做的是什么、怎么用 Spring 实现、按什么顺序做**。
> 标 ⚠️ 的是我跑代码确认过的问题（带行号）；标 🅿️ 的是建议，需要你拍板。

---

## 一、要做的东西

一个本地跑的单机程序：读磁盘上的 5 分钟 K 线（CSV），让用户**像交易当时那样一根一根往下推盘面**，
在推的过程中下单、画图、录音、记笔记；系统同时按 Prop Firm 的考核规则实时算余额、回撤、盈利日，
该升级升级、该淘汰淘汰。

用户的一天是这样过的：

1. 打开账户，选一个品种，选一天 → 盘面停在当天第一根 K 线
2. 按「下一根」，K 线一根根出来；想下单就下单，想标注就画线
3. 每出一根，右上角「距爆仓还有 $482」跟着变，跌破线这一单考核就结束了
4. 收盘后看一遍：今天赚了多少、最多浮亏到哪、这笔交易画出来长什么样

八个业务概念，彼此关系只有一句话：**PF 账户是记账的归属点，成交是唯一的账务事实，其它都是挂在这条时间轴上的东西。**

| 概念 | 是什么 | 归属 |
|---|---|---|
| 标的 `symbol` | 品种（MES / MNQ / MGC），带 tick、tickValue、手续费 | 独立 |
| 模板 `prop_firm_template` | 一套考核规则，三阶段用 `next_template_id` 串起来 | 独立 |
| 账户 `prop_firm_account` | 一次考核。带阶段、规则快照、余额缓存 | 聚合根 |
| 订单 `trade_order` | 挂单意图（限价/止损/市价） | `account_id` + `symbol_id` |
| 成交 `trade` | **账务事实**，一笔成交一行，不可改 | `account_id` + `symbol_id` |
| 交易记录 `trade_record` | 一笔完整交易的摘要（进场、出场、盈亏、MFE/MAE） | `account_id` + `symbol_id` |
| 图形 `drawing` / 语音 `audio_clip` | 标注和复盘录音，挂在某根 K 线上 | 只有 `symbol_id`，不参与账务 |
| 策略 `strategy` / `strategy_bot` | 算法交易，代码里有现成骨架 | `symbol_id` |

---

## 二、三条数据原则

整份方案的地基，其它所有决定都从这儿推。

**1. K 线不进数据库。** 一天一个 CSV，按 `data/<folder>/<date>.csv` 放，只读。
理由：一天 276 行、几年就是几十万行，进库既没必要也不方便 diff；而且这些文件是抓取程序在写的，
库和文件两边都存一份迟早对不上。

**2. 成交是唯一的账务事实。** 持仓数量、持仓均价、已实现盈亏、逐批成本，全部由 `trade` 表按顺序
重算出来，**不单独存一列**。`trade` 写完就不可变（唯一的 setter 是 `tradeRecordId`）。

**3. 能算的不存，必须存的只有「用户进度」。** 余额是缓存（可以随时从阶段起点 + 全部成交重算）；
但「用户看到第几根了」是纯用户状态，推不出来，必须落库。

```
CSV(K线,只读) ──┐
                ├─→ 撮合(纯函数) ─→ 成交 ─→ trade 表
trade_order ────┘                     │
                                      ├─→ 账本重放 → 持仓 / 均价 / 逐批盈亏
                                      ├─→ 考核会话 → 余额 / 峰值 / 盈利日 / 淘汰
                                      └─→ 交易记录 → 一笔交易的摘要（冗余但值得）
```

---

## 三、业务规则（这就是需求本身）

### 3.1 撮合

- 市价单：按**当前 K 线收盘价**立即成交。不滑点、不等下一根——复盘里「现在」就是最后一根的收盘。
- 限价单：买 `本根 low ≤ 挂单价` 成交；卖 `本根 high ≥ 挂单价` 成交。
- 止损单：买 `本根 high ≥ 挂单价` 成交；卖 `本根 low ≤ 挂单价` 成交。
- 成交价：正常情况下就是挂单价；**开盘就已经越过挂单价时取开盘价**。
  限价买取 `min(挂单价, 开盘价)`，止损买取 `max(挂单价, 开盘价)`，卖方向镜像。
  这一条不能省：跳空高开 106 还按 102.5 成交，等于每一笔都白捡钱，回测数字全废。
- 一单要么全成、要么不成，**不做部分成交**。
- **本根挂的单，本根不参与撮合**，从下一根开始。否则就是用已经收完的 K 线的最高最低价倒推成交。
- OCO 组（`oco_group` 非空）：同一根里最多成交一张，其余自动撤销。因为 5 分钟 K 线只有 OHLC，
  分不出高低点谁先到，两张同时触发会把仓位平两遍、多出一个反向仓。
  **组内止损优先于限价**——分不清的时候取对持仓不利的那一侧，否则每次说不清的地方都替策略选好结果。
- 手数只能是整数。

### 3.2 手续费

每个标的自己带 `commission`（**每手每边**，美元），`NOT NULL`，0 就表示免费。
开仓和平仓**都收**，一笔往返等于两次。

成交那一刻把费率**快照**进 `trade.commission`，之后改标的手续费不影响历史账——
否则「上个月的通关成绩」会被今天的配置改写。

**所有考核判定都用扣费后的净额**（`trade.netPnl() = realizedPnl − commission`）。
开仓那一笔 `realizedPnl` 是 0，所以它的净额是负数（只扣了进场费），这是对的，钱确实在开仓时就付了。

### 3.3 PF 考核

三阶段挤在同一行账户上：`EVAL → FUNDED → PAYOUT`，单向，历史写 `prop_firm_stage_log`。
升级时**余额重置回账户规模**（EVAL 赚的钱不带进 FUNDED），所以每个阶段都要存 `base_balance`，
余额不能由成交反推。

- **交易日 = `[D 06:00, 次日 04:45)`**。04:45–06:00 不许交易、也不属于任何交易日。
  不做夏冬令时，CSV 时间列是本地明文，直接当字符串比较。
- **爆仓逐根判**：权益 = 余额 + 所有浮盈亏，浮亏用本根的**最不利价**（多单看 low、空单看 high）。
  用收盘价会漏判——一根长下影线可以盘中穿线再收回来。**等于亏损线也算失败**。
- **亏损线只在日终上移**（EOD 追踪），盘中赚到新高不算。峰值达到 `trailing_lock_level` 后冻结。
- **一致性**：`目标 = max(模板目标, 单日最大净盈利 ÷ 一致性比例)`。
  单日赚 1800、比例 50% → 目标从 3000 抬到 3600。所以「总盈利 3000 但有一天赚了 1800」不能通关。
- **FUNDED 手数按利润分档**（0→20 / 1000→30 / 2000→40），按品种净持仓判。
- **PAYOUT 只是「不能再下单」**，账户仍然 ACTIVE（还能画图录音）。所以 `status` 只有 ACTIVE / FAILED。

### 3.4 三套时间口径，别混

| 时刻 | 语义 | 动作 |
|---|---|---|
| 23:55 | 日历日强平，不留隔夜仓 | 反向市价平掉 + 撤该品种所有挂单 |
| 04:45–06:00 | 禁交易窗，不属于任何交易日 | 这些 K 线全部跳过 |
| 04:50 | 交易日结算收尾 | 同上，但要触发日终结算 |

---

## 四、四个流程（其它都是旁支）

### 流程 1：推进一根 —— 这是整个系统的心脏

```java
@Transactional
public AdvanceResult advance(String accountId, String symbolId) {
    account.requireTradable();                       // 领域闸门：账户在不在、能不能下单
    Cursor cur = cursors.load(accountId, symbolId);
    Bar next = bars.next(cur);                        // 越界就换下一个 CSV；再没有就结束

    // ① 撮合：纯函数，只回答「哪几张单成交了、成交价多少」
    List<Match> matches = Matcher.match(orders.open(accountId, symbolId), next, cur.index());

    // ② 落库：订单状态 + 成交（seq 和手续费快照在这里定）
    for (Match m : matches) {
        order.markFilled(next.barTime(), cur.index() + 1, m.fillPrice());
        trades.save(m.toTrade(accountId, symbolId, symbol.commission()));
    }

    // ③ 推进考核
    Step step = sessions.of(accountId).onBar(symbolId, next);

    // ④ 结果落库
    cur.moveTo(next, cur.index() + 1);
    account.syncComputed(step.balance(), step.eodPeak());
    if (step.event() == FAILED)   accountService.fail(accountId, session);
    if (step.event() == PROMOTED) accountService.promote(accountId, session);
    return new AdvanceResult(step, matches);
}
```

**一个方法一个事务。** 成交、订单状态、游标、账户缓存必须一起成立；CSV 被 Excel 占用就整根回滚，
用户重试即可。

**撮合写成静态纯函数的好处**：单测可以直接喂手工构造的 `Bar`，不启 Spring、不建库、不读文件。

### 流程 2：下单

```java
@Transactional
public Order marketOrder(String accountId, String symbolId, Side side, int qty) {
    account.requireTradable();
    int index = cursors.require(accountId, symbolId);   // 还没开始推进就拒绝
    Bar bar = bars.current(index);
    Order o = orders.save(Order.place(accountId, symbolId, side, MARKET, bar.close(), qty,
                                      bar.barTime(), index, null));
    o.markFilled(bar.barTime(), index, bar.close());
    trades.save(/* 成交，realizedPnl 由账本算 */);
    syncAccount(account, sessions.of(accountId).onBar(symbolId, bar));  // 同一根重算一次
    return o;
}
```

**挂单**（限价/止损）只写 `trade_order`，不动余额、不碰考核 —— 没成交就不是账务事件。

### 流程 3：日终 / 升级 / 淘汰

不写新逻辑，只接上调用方：`session.onBar` 跨交易日时内部结算，返回 `PROMOTED` / `FAILED` 时
由流程 1 调 `PropFirmAccountService`。

**必须有一个 `finish()` 入口**（用户停在最后一根时调一次），否则最后一个交易日不结算、
峰值不更新、盈利日不计数，EVAL 达标也不升级。

### 流程 4：收尾

就是流程 2 的封装：反向市价平掉当前持仓 + 撤销该品种所有 OPEN 单，**先平后撤**。
倒过来做的话，一旦平仓失败，就变成「挂单没了、仓还在」——用户最不想要的状态。

---

## 五、第一步：修三处裂缝 + 补游标表

> ✅ **已完成（2026-10-29）**。`trade.seq` 已加并接管全部排序、`applyNetPnl` 已删、
> `Order.place` 统一从 OPEN 起步、`replay_cursor` 表与实体已建，`./mvnw test` 56 个用例全绿。
> 旧库已改名 `db/tradingview-j.db.bak-20261029-trade-seq` 并重建。

这四处都在现有代码里，跟新功能无关，但不修会一路错下去。

### ⚠️ 1. 同一根 K 线上的多笔成交，顺序是随机的

`PropFirmSession:115`：

```java
sorted.sort(Comparator.comparing(Trade::getBarTime).thenComparing(Trade::getId));
```

`getId()` 是 UUID。同一根 K 线上有多笔成交时（止损平仓 + 反手开仓、两个品种同一时刻），
**谁先谁后由 UUID 的字典序决定**，跟真实发生顺序无关。均价和已实现盈亏会在边界上算错，
而且**每次重启算出来的错还不一样**——这种 bug 最难查。

修法：`trade` 加 `seq INTEGER NOT NULL`，含义「同一 `(account_id, symbol_id)` 内的成交流水序号」，
写入时在同一事务里取 `max(seq)+1`。排序改成 `comparing(symbolId).thenComparing(seq)`。

### ⚠️ 2. 余额有两条算路，其中一条会漏手续费

`PropFirmAccount:161` 的 `applyNetPnl`（增量累加）和 `:178` 的 `syncComputed`（全量追平）并存。
按 `applyNetPnl` 注释写的「每次平仓调一次」来用，**开仓那笔的手续费就丢了**：
开仓 `realizedPnl=0`，净额是 `-commission`，只在平仓调就只扣了一半。往返一笔差一个单边费率。

修法：**删掉 `applyNetPnl`**，余额只允许由 `syncComputed` 追平重放结果。
写进注释：想让余额变，产生成交，不要改余额。

### ⚠️ 3. 市价单走不到 `markFilled`

`Order:121` 在建市价单时直接置 `status = FILLED`，而 `markFilled:131` 要求 `status == OPEN`，
否则抛异常 —— 市价单的成交价、成交下标只能由调用方直接调 setter 硬填，或者留空。

修法：`place()` 一律建 `OPEN`，市价单在下单用例里紧接着 `markFilled(...)`。
状态机只剩一条路：`OPEN → FILLED | CANCELLED`。

### 🅿️ 4. 补一张游标表

15 张表里没有任何地方存「用户看到第几根」，关掉程序就没法接着复盘。

```sql
CREATE TABLE IF NOT EXISTS replay_cursor (
    account_id TEXT NOT NULL,
    symbol_id  TEXT NOT NULL,
    bar_time   TEXT NOT NULL,     -- 已经看到的最后一根（明文，和别处一致）
    bar_index  INTEGER NOT NULL,  -- 当天文件内的下标，0 起
    day_file   TEXT NOT NULL,     -- 当前读的是哪一天的 CSV
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (account_id, symbol_id)
);
```

不放进 `prop_firm_account`：一个账户同时做 MES / MNQ / MGC，每个品种进度不一样，
加列就得加 N 列或存 JSON，两个都不好查。

**改表结构要重建 `./db/`**（`schema.sql` 只有 `CREATE IF NOT EXISTS`）：旧库改名
`db/tradingview-j.db.bak-日期-原因`，别删。

---

## 六、考核会话为什么必须常驻内存

`PropFirmSession` 的 `eodPeak` / `profitDays` / `maxDayNet` 是逐根累积出来的，
**没法从成交流水单独推出来**（盈利日靠日终结算、峰值靠 EOD 快照）。所以只有一种做法：

```java
@Service
public class SessionRegistry {
    private final Map<String, PropFirmSession> live = new ConcurrentHashMap<>();

    public PropFirmSession of(String accountId);  // 懒加载：从阶段起点重放全部 K 线 + 成交
    public void rebuild(String accountId);        // 兜底：任何时候强制重建
    public void evict(String accountId);          // 升阶 / 删账户时必须调
}
```

三条纪律：

1. **重建和实时走同一段代码**（都用 `onBar` 逐根喂），否则两条路会分叉。
2. **成交只能经由 `TradingService` 产生**。绕过它直接 `tradeRepository.save`，会话就是脏的。
3. **升阶时必须 `evict`**：`base_balance` 变了，旧的累积量不能带进新阶段。

冷启动重建要跑 5500 根 × 几百笔（`equity()` 每根重放一次账本），预计 1–3 秒，只发生一次。
🅿️ 第一次跑通后量一下真实耗时，超过 5 秒再考虑给 `equity()` 加增量缓存。

---

## 七、实现顺序

原则：**每一步结束时系统都能跑，并且能证明它对**。不要按层铺（先写一堆 Controller + CRUD），
那样会把已经写好的领域规则整个绕过去。

### 第一步：补裂缝（不改任何行为）

- `trade.seq` + 排序改 `seq`
- 删 `applyNetPnl`
- `Order.place` 统一走 `OPEN → markFilled`
- 加 `TradeLedgerConsistencyTest`：任意成交列表下，`Σ trade.realizedPnl` 必须等于
  `TradeLedger.replay(...).realizedPnl()` —— 把「一处算、两处用」钉死

**验收**：`./mvnw test` 全绿；重建 `./db/` 后 `PersistenceSmokeTest` 通过。

### 第二步：只推进，不交易

`BarSeries`（把一天的 CSV 读成可下标访问的列表）+ `replay_cursor` + `SessionRegistry`
+ `POST /advance` + `POST /session/finish`。

这一步**完全不碰撮合**，就能让考核会话第一次跑在真实 CSV 上。

**验收**：
- 推完 2026-10-16 全部 276 根，每根 `RUNNING`、无 `FAILED`
- `finish()` 后盈利日 = 0、余额 = `base_balance`
- 把 `max_drawdown` 改成极小值 → 某一根 `FAILED`，且 `account.status=FAILED`、
  `prop_firm_stage_log` 多一行 `end_reason=FAILED`
- 单根推进 < 20ms（不含首次加载）

### 第三步：市价单

`TradingService.marketOrder` + `POST /orders/market`。唯一不需要撮合表的成交路径。

**验收**：MES 买 1 手 @ 收盘 → `realizedPnl=0`、`commission` 等于标的值、余额少一个单边手续费；
平掉后净盈亏 = `(平−开) × 5 × 1 − 2 × 手续费`。

### 第四步：挂单撮合

`Matcher` + 下单 / 撤单 / 改价三个接口。**测试全部用手工构造的 `Bar`**，不读文件、不启 Spring。

**验收**（对着 §3.1 的每一条）：
- 限价买挂 100，下一根 `low=99` → 成交在 100
- 同挂 100，但下一根 `open=98` → 成交在 98（跳空不占便宜）
- 止损买挂 100，`open=106` → 成交在 106（不白捡）
- OCO 组内止损挂在限价之后 → 仍判止损成交
- 本根挂的单，本根不参与撮合

### 第五步：多品种 + 跨日 + 收尾

换日文件、两种 `closeOut`（23:55 / 04:50）、禁交易窗跳过。

**验收**：两品种交替推进、同刻一多一空 → 权益各用自己最不利价、互不干扰；
推过 04:45–06:00 的 12 根 → 全部跳过；跨日时峰值和盈利日正确累计。

### 第六步：交易记录自动开始 / 结束

`TradeRecord` 字段已经齐了（`anchorBarTime` / `openPosition` / `closeAt`）。
规则：**空仓 → 有仓开始，回到空仓收尾**，锚点是「开仓成交之前那一根」（记录的第一屏应该是
「还没进场」的样子，不是「已经进场」）。MFE/MAE 在收尾时全量重放，
口径上**排除有成交的那几根 K 线**（一根只有 OHLC，有成交那根的高低点等于替交易者挑时机）。

**验收**：一根 K 线内开了又平 → 一条完整记录；从空到空推 300 根 → 不产生记录。

### 第七步：图形 / 语音

只挂 `(symbol_id, bar_time)`，不参与任何账务，最独立，放最后。

---

## 八、暂不做

- 多人同步 / WebSocket 广播：HTTP + 本人使用就够，多端同步以后再说
- 部分成交、滑点、盘口、盘前盘后
- `TRAILING_INTRADAY` 回撤（`PropFirmSession:357` 现在显式抛异常，保持）
- 策略机器人自动下单

---

## 九、要你定的两件事

1. **游标粒度**：现在设计成 `(账户, 品种)`。要不要支持「同一账户同一品种开两个不同进度的盘面」
   （比如两天前那笔重做一遍）？要的话得加 `replay_id`，复杂度上一档，但改表就是现在最便宜。
2. **跨日推进**：一次请求推一天，还是后端自动串起来？自动串更省事，但一次可能触发几百笔成交
   和多次日终结算，接口就变重了。
