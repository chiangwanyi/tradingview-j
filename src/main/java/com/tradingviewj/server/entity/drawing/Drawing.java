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

    /**
     * AB=CD 的中间点 B。<strong>0 或 1 项</strong>，其它图形这里是空集合。
     *
     * <p>为什么是子表而不是 {@code drawing} 上的三列：{@code schema.sql} 全是
     * {@code CREATE ... IF NOT EXISTS}，老库加列必须另写 ALTER（SQLite 没有
     * {@code ADD COLUMN IF NOT EXISTS}），而 {@code drawing} 里已有用户画的线不能重建。
     * 新表对老库是纯增量，和 {@code drawing_fib_level} 同一个理由。
     */
    @ElementCollection(fetch = FetchType.EAGER)
    @CollectionTable(name = "drawing_mid", joinColumns = @JoinColumn(name = "drawing_id"))
    @AttributeOverride(name = "offset", column = @Column(name = "bar_offset", nullable = false))
    private List<Anchor> mids = new ArrayList<>();

    /** 中间点（非 AB=CD 图形返回 {@code null}）。 */
    public Anchor mid() {
        return mids.isEmpty() ? null : mids.get(0);
    }

    public void setMid(Anchor mid) {
        mids.clear();
        if (mid != null) {
            mids.add(mid);
        }
    }

    /** 端点是否吸附过某根 K 线的 OHLC（界面上画个小圆点提示）。 */
    @Setter
    @Column(nullable = false)
    private boolean snapped;

    /**
     * 一图形多颜色时的那几行：斐波那契四条水平（比例 -1/0/1/2）、AB=CD 三段
     * （比例 0/1/2 = AB/BC/CD）。其它图形这里是空集合。
     */
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
     * 用<strong>前端给的 id</strong>建一个图形。
     *
     * <p>为什么这个聚合允许客户端指定主键（别的实体都让数据库发）：
     * 图形画下去那一刻界面上就要有它 —— 选中、拖动命中测试、删除都按 id 走。
     * 等一个往返还回 id，新线会在那一瞬间「点不中」，而用户的手已经按在它上面了。
     *
     * <p>重号由调用方校验（{@code AnnotationService}）：重号的后果不是报错，
     * 是「拖一条线，另一条跟着动」—— 那种问题事后极难对上号。
     *
     * @throws IllegalArgumentException id 为空
     */
    public static Drawing of(String id, String symbolId, Kind kind,
                            Anchor start, Anchor end, String createdBy) {
        Drawing drawing = of(symbolId, kind, start, end, createdBy);
        if (id == null || id.isBlank()) {
            throw new IllegalArgumentException("用前端给的 id 建图形时 id 不能为空");
        }
        drawing.useId(id);
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

    /** 三个点的图形（AB=CD）整体挪动 / 拖端点：中间点一起走，不然 B 会留在原地。 */
    public void moveTo(Anchor start, Anchor mid, Anchor end) {
        moveTo(start, end);
        setMid(mid);
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
        FIB,
        /** AB=CD：A/B/C 三个点，D = C + (B - A)（AB 平移一段，起点落在 C 上）。 */
        ABCD
    }
}
