package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.trade.ClosedLot;
import com.tradingviewj.server.entity.trade.Position;
import com.tradingviewj.server.entity.trade.Side;
import com.tradingviewj.server.entity.trade.Trade;
import lombok.extern.slf4j.Slf4j;

import java.math.BigDecimal;
import java.math.MathContext;
import java.math.RoundingMode;
import java.util.ArrayList;
import java.util.List;

/**
 * 交易账本：把一串<strong>成交流水</strong>重放成持仓、已实现盈亏和逐批归因。
 *
 * <p><strong>这是整个交易域唯一一处「怎么算」的地方。</strong> 持仓、盈亏、平仓虚线的坐标
 * 全部从这里来，所以界面上三处显示的数永远是同一套算法算的 —— 这在 Go 版里是踩过坑的：
 * 前端曾经自己按手数平摊盈亏，结果 5 笔不同价位的空单显示成 5 个一模一样的数。
 *
 * <p>它是个纯函数式的类（没有 Spring 注解、没有状态、没有依赖），入参是成交列表 +
 * 每点价值，出参是结果对象。这让它在单测里造几条成交就能验，不需要起数据库、不需要起 Web。
 *
 * <p><strong>两种口径同时算</strong>，因为它们回答的是不同的问题：
 *
 * <ul>
 *   <li><strong>平均成本法</strong>（{@code position} / {@code realizedPnl}）——
 *       「我现在持多少、均价多少、总共赚了多少」。账户和考核只看这一套。</li>
 *   <li><strong>FIFO 逐批</strong>（{@code closedLots}）—— 「屏幕上这根平仓线，是哪一手在哪个价开的、
 *       它自己赚了多少」。画线和信息卡只看这一套。</li>
 * </ul>
 */
@Slf4j
public final class TradeLedger {

    /** 均价除法的精度。8 位足够：价格本身只有两位小数，手数最多四位。 */
    private static final MathContext AVG_CONTEXT = new MathContext(10, RoundingMode.HALF_UP);

    private TradeLedger() {
    }

    /**
     * 每点价值：价格每走 1.0，一手值多少美元。
     *
     * <p>由 {@code tickValue / tick} 得来（MNQ：0.5 / 0.25 = 2；CL：10 / 0.01 = 1000）。
     * 任一项缺失或非正时<strong>退化成一个点 1 美元</strong> —— 明确退化好过抛异常：
     * 没配置刻度的标的一样要能复盘，只不过程度上「盈亏数字不准」，界面上要提示。
     */
    public static BigDecimal pointValue(BigDecimal tick, BigDecimal tickValue) {
        if (tick == null || tickValue == null || tick.signum() <= 0 || tickValue.signum() <= 0) {
            return BigDecimal.ONE;
        }
        return tickValue.divide(tick, AVG_CONTEXT);
    }

    /**
     * 一笔成交<strong>实现了多少</strong>（平均成本法，毛利，未扣手续费）。
     *
     * <p>开仓 / 同向加仓返回 0：加仓不产生盈亏，只改成本。反向成交按
     * {@code (成交价 - 均价) × 平掉的手数 × 原仓位方向 × 每点价值} 记。
     * 手数超过持仓时（反手）只算能平掉的那部分，剩下的算新开仓 —— 由调用方按
     * {@link #replay} 的同一套规则处理。
     *
     * <p>它同时被 {@link #replay} 和下单服务（{@code TradingService}）使用：
     * 前者逐笔重放历史，后者只算「刚落下的这一笔」。两处共用这一个算式，
     * 「成交流水算出来的盈亏」和「成交那一刻写进 trade 的 realizedPnl」就不可能对不上。
     *
     * @param position      成交<strong>之前</strong>的净头寸（均价必须有效）
     * @param fillPrice     实际成交价（不是挂单价，跳空时两者不同）
     * @param signedQuantity 带符号的成交量：买为正、卖为负
     */
    public static BigDecimal realizedFor(Position position, BigDecimal fillPrice,
                                        BigDecimal signedQuantity, BigDecimal pointValue) {
        if (position == null || position.isFlat() || signedQuantity == null
                || signedQuantity.signum() == 0 || pointValue == null) {
            return BigDecimal.ZERO;
        }
        BigDecimal held = position.quantity();
        if (held.signum() == signedQuantity.signum()) {
            return BigDecimal.ZERO;
        }
        BigDecimal closeQty = held.abs().min(signedQuantity.abs());
        BigDecimal dir = BigDecimal.valueOf(held.signum());
        return fillPrice.subtract(position.averagePrice())
                .multiply(closeQty).multiply(dir).multiply(pointValue);
    }

    /**
     * 重放成交。
     *
     * @param trades     按时间顺序排列的成交（顺序错了结果就错了，调用方负责排序）
     * @param pointValue 每点价值，见 {@link #pointValue}
     */
    public static Ledger replay(List<Trade> trades, BigDecimal pointValue) {
        List<ClosedLot> closedLots = new ArrayList<>();
        List<Lot> lots = new ArrayList<>();
        BigDecimal qty = BigDecimal.ZERO;
        BigDecimal avgPrice = BigDecimal.ZERO;
        BigDecimal realized = BigDecimal.ZERO;

        for (Trade trade : trades) {
            BigDecimal signed = trade.signedQuantity();
            BigDecimal prev = qty;

            if (prev.signum() == 0 || prev.signum() == signed.signum()) {
                // 空仓开仓 / 同向加仓：记一批新的，**不摊平已有批次的价格**
                if (prev.signum() == 0) {
                    lots.clear();
                }
                lots.add(new Lot(trade.getBarTime(), trade.getPrice(), signed.abs()));

                BigDecimal total = prev.abs().add(signed.abs());
                if (total.signum() > 0) {
                    avgPrice = prev.abs().multiply(avgPrice).add(signed.abs().multiply(trade.getPrice()))
                            .divide(total, AVG_CONTEXT);
                }
                qty = prev.add(signed);
                continue;
            }

            // 反向成交 = 平仓：先按平均成本记掉这一笔实现的盈亏。
            // 算式在 realizedFor 里只有一份 —— 下单时的「这一笔赚多少」也调它，
            // 两处各写一遍迟早会在反手/部分平仓上分叉，而那种偏差不抛异常。
            realized = realized.add(realizedFor(
                    new Position(prev, avgPrice), trade.getPrice(), signed, pointValue));

            // 再按 FIFO 从最早的批次开始消耗，消耗几批就产出几条归因
            Side lotSide = prev.signum() > 0 ? Side.BUY : Side.SELL;
            BigDecimal dirs = BigDecimal.valueOf(prev.signum());
            BigDecimal left = signed.abs();
            while (left.signum() > 0 && !lots.isEmpty()) {
                Lot lot = lots.get(0);
                BigDecimal take = lot.remaining.min(left);
                closedLots.add(new ClosedLot(
                        lot.barTime, lot.price,
                        trade.getBarTime(), trade.getPrice(),
                        lotSide, take,
                        trade.getPrice().subtract(lot.price).multiply(take).multiply(dirs).multiply(pointValue)));
                lot.remaining = lot.remaining.subtract(take);
                left = left.subtract(take);
                if (lot.remaining.signum() <= 0) {
                    lots.remove(0);
                }
            }

            BigDecimal next = prev.add(signed);
            if (next.signum() == 0) {
                qty = BigDecimal.ZERO;
                avgPrice = BigDecimal.ZERO;
                lots.clear();
            } else {
                qty = next;
                if (next.signum() != prev.signum()) {
                    // 反手：剩下的部分是这一笔新开的仓，成本就是本笔成交价
                    avgPrice = trade.getPrice();
                    lots.clear();
                    lots.add(new Lot(trade.getBarTime(), trade.getPrice(), next.abs()));
                }
            }
        }

        return new Ledger(new Position(qty, avgPrice), realized, closedLots);
    }

    /**
     * 重放结果。
     *
     * @param position    净头寸（平均成本法）
     * @param realizedPnl 已实现盈亏合计（平均成本法，美元）
     * @param closedLots  逐批归因，按平仓顺序排列
     */
    public record Ledger(Position position, BigDecimal realizedPnl, List<ClosedLot> closedLots) {
    }

    /** 一批还没平完的仓位（内部用，可变）。 */
    private static final class Lot {
        private final String barTime;
        private final BigDecimal price;
        private BigDecimal remaining;

        private Lot(String barTime, BigDecimal price, BigDecimal quantity) {
            this.barTime = barTime;
            this.price = price;
            this.remaining = quantity;
        }
    }
}
