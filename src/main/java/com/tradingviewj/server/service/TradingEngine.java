package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.trade.Order;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.storage.Bar;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * 撮合规则与价格/手数校验的<strong>唯一实现</strong>（Go 版对应 {@code internal/replay/instance.go} 里
 * {@code orderTriggered} / {@code fillPriceFor} / {@code ocoWinner} / {@code roundPrice} 那几个函数）。
 *
 * <h2>为什么单独一个类</h2>
 *
 * <p>这几条规则不需要数据库、不需要 Spring、不需要账户 —— 入参是「一张单 + 一根 K 线」，
 * 出参是布尔或者一个价格。放在 {@code TradingService} 里也行，但那样每验一条撮合规则
 * 都要先建库、建账户、造 CSV；而撮合恰恰是<strong>最需要被逐条钉住</strong>的部分
 * （跳空、同根双触发、挂反的单）。分开之后这些用例可以是纯单元测试。
 *
 * <p>同样的理由让它成为<strong>唯一</strong>一份：服务层只调用这里，不自己再写一遍
 * 「最低价 ≤ 挂单价」—— 两份判据在跳空和反手这些边界上一定会分叉，而分叉出来的错
 * 不抛异常，只是数字慢慢不对。
 *
 * <h2>撮合口径（与 Go 版逐条对齐）</h2>
 *
 * <pre>
 * 限价买：本根 low  ≤ 挂单价      限价卖：本根 high ≥ 挂单价
 * 止损买：本根 high ≥ 挂单价      止损卖：本根 low  ≤ 挂单价
 * </pre>
 *
 * <p><strong>成交价不是一律取挂单价</strong>：开盘就跳过挂单价时，那一笔真实成交发生在
 * 开盘那一刻。按挂单价成交等于凭空占便宜（跳空高开 106 却还按 102.5 买到），
 * 而这类偏差只会在跳空日出现 —— 回测数字会系统性偏乐观。规则见 {@link #fillPrice}。
 *
 * <p>不做部分成交、不算滑点：一根 5 分钟 K 线只有 O/H/L/C，没有盘口，
 * 这两件事没有数据支撑，硬编一个模型只会让复盘结论不可复现。
 */
public final class TradingEngine {

    /** 单笔手数上限（和 Go 版 {@code maxOrderQuantity} 同一个数）。 */
    public static final int MAX_QUANTITY = 10000;

    private TradingEngine() {
    }

    /**
     * 这张单有没有被这根 K 线触发。
     *
     * <p>市价单永远不会出现在这里：它在下单那一刻就成交了，不会以 OPEN 状态留到下一根。
     * 真出现（数据坏了）返回 {@code false}，而不是抛异常 —— 一次撮合失败不该让整根 K 线回滚。
     */
    public static boolean triggered(Order order, Bar bar) {
        if (order == null || bar == null || order.getPrice() == null) {
            return false;
        }
        BigDecimal price = order.getPrice();
        boolean buy = order.getSide() == Side.BUY;
        return switch (order.getType()) {
            case LIMIT -> buy
                    ? bar.low().compareTo(price) <= 0
                    : bar.high().compareTo(price) >= 0;
            case STOP -> buy
                    ? bar.high().compareTo(price) >= 0
                    : bar.low().compareTo(price) <= 0;
            case MARKET -> false;
        };
    }

    /**
     * 成交价：正常情况下是挂单价；<strong>开盘就已经越过挂单价时取开盘价</strong>。
     *
     * <p>取 min 还是 max，由「价格从哪一侧穿过来」决定：
     *
     * <pre>
     * 限价买 / 止损卖 → min(挂单价, 开盘价)   价格向下穿，开盘更低就先成交在开盘
     * 限价卖 / 止损买 → max(挂单价, 开盘价)   价格向上穿，开盘更高就先成交在开盘
     * </pre>
     *
     * <p>这两行同时说明了两类单的区别：限价单拿到的是<strong>不比挂单价差</strong>的价
     * （等价格过来），止损单拿到的是<strong>不比挂单价好</strong>的价（价格跑了才追）。
     * 方向写反，复盘里就出现了无风险套利。
     */
    public static BigDecimal fillPrice(Order order, Bar bar, BigDecimal tick) {
        BigDecimal open = bar.open();
        if (open == null || open.signum() <= 0) {
            return order.getPrice();
        }
        boolean downward = (order.getType() == Order.Type.LIMIT) == (order.getSide() == Side.BUY);
        BigDecimal raw = downward ? open.min(order.getPrice()) : open.max(order.getPrice());
        return roundPrice(raw, tick);
    }

    /**
     * 这一根里每个 OCO 组「该成交哪一张」，返回 {@code 组号 → orderId}。
     *
     * <p><strong>同一组最多成交一张</strong>：5 分钟 K 线看不出最高价和最低价谁先出现，
     * 止损和止盈可能同时被判为触发。不拦的话，一张止盈单平完仓，止损单接着成交，
     * 仓位被平过头、反手开出一个谁也不想要的仓。
     *
     * <p><strong>组内优先级：止损 &gt; 限价；同类按挂单时间、再按 id。</strong>
     * 为什么是「止损优先」而不是「止盈优先」：两头都扎到时，数据里根本没有先后，
     * 唯一诚实的做法是取<strong>对持仓更不利</strong>的那一侧。反过来等于每次「说不清」
     * 都替策略挑了好结果 —— 回测好看，实盘一跑就露馅。
     *
     * <p>优先级<strong>显式比较</strong>，不依赖挂单顺序：早先 Go 版靠「谁先挂谁成交」，
     * 因为机器人恰好先挂止损而碰巧正确；改一行挂单顺序就会静默翻转成乐观口径。
     *
     * @param openOrders 本根<strong>可以参与撮合</strong>的挂单（调用方已按「本根挂的本根不撮合」筛过）
     */
    public static Map<String, String> ocoWinners(List<Order> openOrders, Bar bar) {
        Map<String, Order> best = new LinkedHashMap<>();
        for (Order order : openOrders) {
            String group = order.getOcoGroup();
            if (group == null || group.isBlank() || !triggered(order, bar)) {
                continue;
            }
            Order current = best.get(group);
            if (current == null || comparePriority(order, current) < 0) {
                best.put(group, order);
            }
        }
        Map<String, String> winners = new LinkedHashMap<>();
        best.forEach((group, order) -> winners.put(group, order.getId()));
        return winners;
    }

    /** 组内优先级：数字小的先成交。同类型再比挂单时间（定长明文，字典序即时间序），最后比 id 保证确定。 */
    private static int comparePriority(Order a, Order b) {
        int byKind = Integer.compare(priorityRank(a), priorityRank(b));
        if (byKind != 0) {
            return byKind;
        }
        int byTime = a.getPlacedAtBar().compareTo(b.getPlacedAtBar());
        if (byTime != 0) {
            return byTime;
        }
        return a.getId().compareTo(b.getId());
    }

    private static int priorityRank(Order order) {
        return order.getType() == Order.Type.STOP ? 0 : 1;
    }

    /**
     * 把价格吸到最小跳动的整数倍上；没有配置 tick 时按两位小数。
     *
     * <p>为什么要吸：成交价会进 {@code trade.price}，而均价、盈亏、画布上的坐标全部由它派生。
     * 一个 4000.1000000001 这样的价格会让「同一批仓位」在两次重算后差一分钱，
     * 而账目上一分钱的差异没有已知正确的答案。
     *
     * <p>返回值的 scale 取 tick 的 scale（没有 tick 就是 2）：<strong>不做 stripTrailingZeros</strong>
     * —— 那会把 {@code 4000.00} 变成 {@code 4E+3}，JSON 里就成了科学计数法。
     */
    public static BigDecimal roundPrice(BigDecimal price, BigDecimal tick) {
        if (price == null) {
            return BigDecimal.ZERO;
        }
        if (tick == null || tick.signum() <= 0) {
            return price.setScale(2, RoundingMode.HALF_UP);
        }
        int scale = Math.max(tick.scale(), 0);
        BigDecimal steps = price.divide(tick, 0, RoundingMode.HALF_UP);
        return steps.multiply(tick).setScale(scale, RoundingMode.HALF_UP);
    }

    /**
     * 校验手数并转成整数：必须是 1 ~ {@link #MAX_QUANTITY} 的整数。
     *
     * <p>不允许小数手：期货没有「0.4 手」，允许小数会让持仓、成交、平仓配对全线出现半手，
     * 「2 手分两次开仓」也会退化成一条平均成本的线。
     *
     * @throws BusinessException 数量为空 / 非正 / 带小数 / 超上限（消息直接给用户看）
     */
    public static int normalizeQuantity(BigDecimal quantity) {
        if (quantity == null) {
            throw new BusinessException("下单必须给手数");
        }
        if (quantity.signum() <= 0) {
            throw new BusinessException("手数必须大于 0，收到: " + quantity.toPlainString());
        }
        if (quantity.stripTrailingZeros().scale() > 0) {
            throw new BusinessException("手数必须是整数（1、2、3…），收到: " + quantity.toPlainString());
        }
        if (quantity.compareTo(BigDecimal.valueOf(MAX_QUANTITY)) > 0) {
            throw new BusinessException("单笔手数最多 " + MAX_QUANTITY + " 手，收到: " + quantity.toPlainString());
        }
        return quantity.intValueExact();
    }

    /**
     * 校验挂单价：必须大于 0 的有限数，并吸到 tick 上。
     *
     * @throws BusinessException 价格缺失 / 非正
     */
    public static BigDecimal normalizePrice(BigDecimal price, BigDecimal tick) {
        if (price == null) {
            throw new BusinessException("挂单必须给价格");
        }
        if (price.signum() <= 0) {
            throw new BusinessException("挂单价格必须大于 0，收到: " + price.toPlainString());
        }
        return roundPrice(price, tick);
    }

    /**
     * 协议串 → 买卖方向。只收小写 {@code buy} / {@code sell}（和下行广播同一套词），
     * 大写也认，但不认 {@code long} / {@code short} —— 那是仓位方向，不是下单方向。
     *
     * @throws BusinessException 无法识别
     */
    public static Side parseSide(String wire) {
        if (wire == null || wire.isBlank()) {
            throw new BusinessException("下单必须给方向（buy / sell）");
        }
        return switch (wire.trim().toLowerCase(java.util.Locale.ROOT)) {
            case "buy" -> Side.BUY;
            case "sell" -> Side.SELL;
            default -> throw new BusinessException("买卖方向只能是 buy / sell，收到: " + wire);
        };
    }

    /**
     * 协议串 → 挂单类型。这里<strong>只接</strong> {@code limit} / {@code stop}：
     * 市价单走另一条命令（{@code market_order}），它没有「先挂着再等撮合」这一步。
     *
     * @throws BusinessException 无法识别 / 用了 market
     */
    public static Order.Type parsePlacementType(String wire) {
        if (wire == null || wire.isBlank()) {
            throw new BusinessException("挂单必须给类型（limit / stop）");
        }
        return switch (wire.trim().toLowerCase(java.util.Locale.ROOT)) {
            case "limit" -> Order.Type.LIMIT;
            case "stop" -> Order.Type.STOP;
            case "market" -> throw new BusinessException("市价单请用 market_order 提交：它下单即成交，不留在挂单里");
            default -> throw new BusinessException("挂单类型只能是 limit / stop，收到: " + wire);
        };
    }

    /** 同一次撮合里按「本根挂的本根不撮合」筛出的候选单（顺序由调用方给定，OCO 判定不依赖它）。 */
    public static List<Order> eligible(List<Order> openOrders, String barTime) {
        List<Order> out = new ArrayList<>();
        for (Order order : openOrders) {
            if (order.getPlacedAtBar() != null && order.getPlacedAtBar().compareTo(barTime) < 0) {
                out.add(order);
            }
        }
        return out;
    }
}
