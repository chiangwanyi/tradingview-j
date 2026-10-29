package com.tradingviewj.server.entity.drawing;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.AttributeOverride;
import jakarta.persistence.AttributeOverrides;
import jakarta.persistence.CollectionTable;
import jakarta.persistence.Column;
import jakarta.persistence.ElementCollection;
import jakarta.persistence.Embedded;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.OrderColumn;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;

import java.util.ArrayList;
import java.util.List;

/**
 * 画在盘面上的一个图形。
 *
 * <p><strong>职责边界</strong>：它只描述「画了什么、什么样子」。它不算任何东西 ——
 * 斐波那契四条线的价格是渲染时按 start/end 现算的，不存进库：存进去就会和端点脱节，
 * 用户拖一下端点，四条线里有两条的 y 坐标就成历史残留。
 *
 * <p>五种图形（趋势线 / 射线 / 矩形 / 箭头 / 斐波那契）共用同一套「起点 + 终点」结构，
 * 而不是每种图形一个表或一个子类。理由很实在：画笔工具、命中测试、拖动端点、
 * 序列化下发这四件事对五种图形是完全一样的，只有「怎么把两个点画成图形」不同，
 * 那是渲染层的事。用继承或分表换来的是五个几乎相同的类和一堆 JOIN。
 *
 * <p><strong>它不挂在账户上</strong>：图形的附体是 K 线（{@code symbolId} + {@code barTime}），
 * 不参与余额、回撤、一致性、盈利日里的任何一项统计，所以没有 {@code accountId}。
 * 给一条线记着「它属于哪个账户」，换来的行为是「换个账户做同一张图，昨天的线全不见了」——
 * 而它本来就画在同一根 K 线上。只有用户自己点删除才该消失。
 */
@Getter
@Entity
@Table(name = "drawing")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Drawing extends BaseEntity {

    /**
     * 画在哪个品种的时间轴上。
     *
     * <p>图形的坐标只有 {@code {barTime, price}} —— 没有品种就不知道这两条轴属于哪张图，
     * 一根 K 线也只有在某个品种下才是唯一的一根。
     */
    @Column(nullable = false)
    private String symbolId;

    @Enumerated(EnumType.STRING)
    @Column(nullable = false)
    private Kind kind;

    @Embedded
    @AttributeOverrides({
            @AttributeOverride(name = "barTime", column = @Column(name = "start_bar_time", nullable = false)),
            @AttributeOverride(name = "price", column = @Column(name = "start_price", nullable = false)),
            @AttributeOverride(name = "offset", column = @Column(name = "start_offset", nullable = false))
    })
    private Anchor start;

    @Embedded
    @AttributeOverrides({
            @AttributeOverride(name = "barTime", column = @Column(name = "end_bar_time", nullable = false)),
            @AttributeOverride(name = "price", column = @Column(name = "end_price", nullable = false)),
            @AttributeOverride(name = "offset", column = @Column(name = "end_offset", nullable = false))
    })
    private Anchor end;

    @Setter
    @Embedded
    private DrawingStyle style;

    /** 端点是否吸附过某根 K 线的 OHLC（界面上画个小圆点提示）。 */
    @Setter
    @Column(nullable = false)
    private boolean snapped;

    /** 斐波那契的四条水平线。其它图形这里是空集合。 */
    @Setter
    @ElementCollection(fetch = FetchType.EAGER)
    @CollectionTable(name = "drawing_fib_level", joinColumns = @JoinColumn(name = "drawing_id"))
    @OrderColumn(name = "level_index")
    private List<FibLevel> fibLevels = new ArrayList<>();

    @Column(nullable = false, updatable = false)
    private String createdBy;

    /**
     * 画一个图形。
     *
     * @param symbolId  画在哪个品种上，<strong>不能为空</strong>：坐标里没有品种，只有时间和价格，
     *                  一根 K 线只有在某个品种下才唯一
     * @param createdBy 谁画的（人 / 机器人）。这是「谁干的」，不是「算在谁头上」——
     *                  图形不参与账户统计，所以没有账户参数
     * @throws IllegalArgumentException 没给标的
     */
    public static Drawing of(String symbolId, Kind kind,
                            Anchor start, Anchor end, String createdBy) {
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("图形必须画在某个标的上，收到: " + symbolId);
        }
        Drawing drawing = new Drawing();
        drawing.symbolId = symbolId;
        drawing.kind = kind;
        drawing.start = start;
        drawing.end = end;
        drawing.createdBy = createdBy;
        return drawing;
    }

    /**
     * 拖动端点。{@code modify} 时两端都要给 —— 只改一端会让射线/矩形的另一端
     * 停在用户没预期的地方。
     */
    public void moveTo(Anchor start, Anchor end) {
        this.start = start;
        this.end = end;
    }

    public enum Kind {
        /** 两点连成的线段。 */
        TREND,
        /** 从起点往终点方向无限延伸。 */
        RAY,
        /** 两个对角点确定的矩形。 */
        RECT,
        /** 带箭头的线段。 */
        ARROW,
        /** 斐波那契回撤：两个端点只提供价格区间。 */
        FIB
    }
}
