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
 * <p><strong>它不随账户消失</strong>：删掉 PF 账户只是删掉那个统计点，已经画下来的线
 * 一条都不会被级联删除 —— 图形是用户的劳动成果，只有用户自己点删除才该消失。
 * 账户没了之后图上这里只剩 {@code accountId = NULL}，等下次挂到别的账户上再显示。
 */
@Getter
@Entity
@Table(name = "drawing")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class Drawing extends BaseEntity {

    /**
     * 画在哪个 PF 账户的盘面上；NULL = 所在账户已被删除。
     *
     * <p>可空是<strong>故意的</strong>：图形与账户在归属上相互独立，删账户不删图形。
     * 创建时必须有账户（{@link #of} 会拦），可空只用来承接「账户被删」这一个事实。
     */
    @Column
    private String accountId;

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
     * @param accountId 画在哪个 PF 账户的盘面上，<strong>不能为空</strong>：图形只可能是
     *                  考核期里的操作。允许为空的列只是为「账户被删」留的出口
     * @throws IllegalArgumentException 没给账户
     */
    public static Drawing of(String accountId, Kind kind, Anchor start, Anchor end, String createdBy) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("图形必须画在某个 PF 账户里，收到: " + accountId);
        }
        Drawing drawing = new Drawing();
        drawing.accountId = accountId;
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
