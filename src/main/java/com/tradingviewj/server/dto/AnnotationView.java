package com.tradingviewj.server.dto;

import com.tradingviewj.server.entity.drawing.Anchor;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.entity.drawing.DrawingStyle;
import com.tradingviewj.server.entity.drawing.FibLevel;

import java.util.List;
import java.util.Locale;

/**
 * 一个图形<strong>在协议里</strong>的样子（前端 {@code webapp/src/types/protocol.ts}
 * 的 {@code Annotation}）。
 *
 * <h2>为什么不让 {@link Drawing} 实体直接上线</h2>
 *
 * <p>三个字段的形状就对不上，而且是<strong>三个方向</strong>都不一样：
 *
 * <ul>
 *   <li>{@code kind}：实体是枚举 {@code TREND}，协议是小写 {@code trend}；</li>
 *   <li>{@code start/end}：实体叫 {@code barTime}，协议叫 {@code time}；</li>
 *   <li>{@code style.dash}：实体是 {@code SOLID}，协议是 {@code solid}；
 *       斐波那契的四条线在实体上是 {@code fibLevels}（独立字段、占表行），
 *       在协议上却在 {@code style.levels} 里面。</li>
 * </ul>
 *
 * <p>不靠 {@code @JsonProperty} 改实体：那会让「改一次协议」变成「改一张表」，
 * 而库里存的枚举名是审计口径，不该为客户端格式让步（见 {@code schema.sql} 第 5 条）。
 *
 * <p>刻意<strong>不发 {@code symbolId}</strong>：一条广播只属于一块盘面
 * （连接所在的账户已经绑定了唯一标的，快照里也给了）。每个图形再各带一份，
 * 前端就会开始相信图形上那个标的 —— 而它与账户不一致时没人能解释。
 *
 * @param createdAt 创建时刻，<strong>Unix 毫秒</strong>（和 {@code Instant} 一样）。
 *                  前端只拿它排列表顺序，不做展示。
 */
public record AnnotationView(
        String id,
        String kind,
        AnnotationPointView start,
        /** AB=CD 的 B 点；其它图形没有这个字段（全局 non_null 会省掉这一键）。 */
        AnnotationPointView mid,
        AnnotationPointView end,
        AnnotationStyleView style,
        boolean snapped,
        String createdBy,
        long createdAt
) {

    /** 实体 → 协议。{@code createdAt} 为空时给 0：图形总归有创建时间，这只是兜底。 */
    public static AnnotationView of(Drawing drawing) {
        return new AnnotationView(
                drawing.getId(),
                lower(drawing.getKind()),
                point(drawing.getStart()),
                point(drawing.mid()),
                point(drawing.getEnd()),
                style(drawing.getStyle(), drawing.getFibLevels()),
                drawing.isSnapped(),
                drawing.getCreatedBy(),
                drawing.getCreatedAt() == null ? 0L : drawing.getCreatedAt().toEpochMilli());
    }

    /**
     * 样式 + 斐波那契水平线合到一份协议样式里。
     *
     * <p>{@code levels} 只有 FIB 图形才有值 —— 给一条趋势线发 {@code []} 也不算错，
     * 但发 {@code null}（全局 non_null 会省掉这个键）更诚实：「这个图形没有水平线」。
     */
    static AnnotationStyleView style(DrawingStyle style, List<FibLevel> levels) {
        return new AnnotationStyleView(
                style == null ? null : style.getColor(),
                style == null ? null : style.getWidth(),
                style == null || style.getDash() == null ? null : lower(style.getDash()),
                style == null ? null : style.getFill(),
                style == null ? null : style.getFillOpacity(),
                levels == null || levels.isEmpty() ? null : levels.stream()
                        .map(level -> new AnnotationStyleView.FibLevelView(
                                level.getRatio() == null ? 0 : level.getRatio().intValue(),
                                level.getColor()))
                        .toList());
    }

    private static AnnotationPointView point(Anchor anchor) {
        return anchor == null
                ? null
                : new AnnotationPointView(anchor.getBarTime(), anchor.getPrice(), anchor.getOffset());
    }

    /** 枚举 → 协议串。{@code Locale.ROOT} 的理由同 {@code ReplayAction#wire()}（土耳其语的 I 问题）。 */
    private static String lower(Enum<?> value) {
        return value == null ? null : value.name().toLowerCase(Locale.ROOT);
    }
}
