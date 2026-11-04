package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.AnnotationStyleView;
import com.tradingviewj.server.entity.drawing.Drawing;
import com.tradingviewj.server.exception.BusinessException;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * 绘图样式的<strong>合法范围</strong>，以及协议串 ↔ 枚举的换算。两个 service 共用一份
 * （{@code AnnotationService} 画图时用、{@code AnnotationTemplateService} 存模板时用）。
 *
 * <h2>为什么夹取放在后端</h2>
 *
 * <p>前端 {@code annotations.ts} 的 {@code resolveStyle()} 已经夹过一遍了，这里再夹一遍是
 * <strong>刻意的重复</strong>：前端的夹取是为了「画得出来」，后端的夹取是为了「存得进去的东西
 * 一定是能读回来的」。手改 localStorage、直接用 curl 往 WS 里灌一条消息、或者以后接一个
 * 命令行客户端，都能绕过前端 —— 到那时候库里就会出现一条 {@code width: 400} 的线，
 * 前端拿到它时 {@code resolveStyle} 会悄悄改成 6，于是「存的和看到的」永远不一致。
 *
 * <p>两边规则必须同源：改这里要同步改 {@code webapp/src/components/chart/annotations.ts}
 * 的 {@code resolveStyle} / {@code defaultStyle}（和 {@code lib/session.ts} 与
 * {@code PropFirmClock} 那两份口径是同一个约定）。
 */
final class AnnotationStyleRules {

    /** 斐波那契的四个比例。<strong>顺序就是显示顺序</strong>，前端 {@code FIB_RATIOS} 是同一份。 */
    static final List<Integer> FIB_RATIOS = List.of(-1, 0, 1, 2);

    /** 四条水平线的出厂色，按 ratio 索引。前端 {@code FIB_COLORS} 是同一份。 */
    static final Map<Integer, String> FIB_COLORS = Map.of(
            -1, "#38bdf8",
            0, "#f59e0b",
            1, "#ec4899",
            2, "#22c55e");

    /** 线条出厂色（五种工具共用一个）。 */
    static final String DEFAULT_COLOR = "#f59e0b";

    /** 线宽下限 / 上限（屏幕像素）。1px 以下的线在黑色蜡烛上基本看不见，6px 盖住 K 线。 */
    static final int MIN_WIDTH = 1;
    static final int MAX_WIDTH = 6;

    /** 矩形填充不透明度的范围。 */
    static final BigDecimal MIN_FILL_OPACITY = new BigDecimal("0.02");
    static final BigDecimal MAX_FILL_OPACITY = new BigDecimal("0.6");
    static final BigDecimal DEFAULT_FILL_OPACITY = new BigDecimal("0.12");

    /** 模板名的上限，和界面输入框的 {@code maxlength="24"} 同一个数。 */
    static final int MAX_NAME_LENGTH = 24;

    /** 单个工具的模板上限：列表是全量广播的，攒到几百条会拖慢每一次操作。 */
    static final int MAX_TEMPLATES_PER_KIND = 20;

    /**
     * 颜色只认 {@code #rgb} / {@code #rrggbb}。
     *
     * <p>不认 {@code red} 这类具名色、也不认 {@code rgb(...)}：这串值会直接进 SVG 的
     * {@code stroke}，拼错一个分号就是属性注入 —— 前端 {@code barcount.ts} 里对颜色
     * 卡同样的正则，理由相同。手改过的存档是这里唯一要防的东西。
     */
    private static final Pattern COLOR = Pattern.compile("^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$");

    private AnnotationStyleRules() {
    }

    /** 五种绘图工具在协议里的名字，用来给「没填名字」的模板兜个底。 */
    private static String label(Drawing.Kind kind) {
        return switch (kind) {
            case TREND -> "趋势线";
            case RAY -> "射线";
            case RECT -> "矩形";
            case ARROW -> "箭头线";
            case FIB -> "斐波那契";
            case ABCD -> "AB=CD";
        };
    }

    /**
     * 协议串 → 枚举（{@code "trend"} → {@code TREND}）。
     *
     * <p>用 {@code valueOf(name.toUpperCase(ROOT))} 而不是手写 switch：两个方向只有一份枚举，
     * 加第六种工具时这里自动跟上（手写 switch 会静默漏掉，而漏掉的表现是「新工具画不出线」）。
     *
     * @throws BusinessException 这串不是任何一种绘图工具
     */
    static Drawing.Kind kindOf(String wire) {
        if (wire == null || wire.isBlank()) {
            throw new BusinessException("缺少 kind：图形和模板都必须说明是哪种绘图工具");
        }
        try {
            return Drawing.Kind.valueOf(wire.trim().toUpperCase(Locale.ROOT));
        } catch (IllegalArgumentException e) {
            throw new BusinessException("不认识的绘图工具「" + wire + "」：只能是 "
                    + Set.of(Drawing.Kind.values()));
        }
    }

    /** 枚举 → 协议串。{@code Locale.ROOT} 的理由同 {@code ReplayAction#wire()}（土耳其语的 I 问题）。 */
    static String wire(Drawing.Kind kind) {
        return kind == null ? null : kind.name().toLowerCase(Locale.ROOT);
    }

    /**
     * 把「可能不全 / 可能越界」的样式夹进合法范围。
     *
     * <p>缺什么补什么（出厂值），错的就丢掉换成出厂值 —— <strong>不抛异常</strong>。
     * 理由：样式是外观，一条线宽写成 0 不值得让整次画图失败；而「夹到合法值」这件事
     * 用户看不出差别（1.4 → 1.4，400 → 6 顶多觉得线有点粗），报错反而莫名其妙。
     * 真正该拒绝的（kind 不认识、锚点缺失）在别处拒绝。
     */
    static AnnotationStyleView sanitize(Drawing.Kind kind, AnnotationStyleView in) {
        AnnotationStyleView style = in == null ? new AnnotationStyleView(null, null, null, null, null, null) : in;

        String color = color(style.color(), DEFAULT_COLOR);
        BigDecimal width = width(style.width(), kind);
        String dash = "dashed".equalsIgnoreCase(style.dash()) ? "dashed" : "solid";

        if (kind == Drawing.Kind.RECT) {
            // 矩形没有单色「线色 + 填充色」的区分时，填充跟随边框 —— 和前端 defaultStyle 一致
            return new AnnotationStyleView(color, width, dash,
                    color(style.fill(), color),
                    fillOpacity(style.fillOpacity()),
                    null);
        }
        if (kind == Drawing.Kind.FIB) {
            // 斐波那契没有「一个颜色」：四条线各一个。顶层 color 留着给 chip 上的色块用
            return new AnnotationStyleView(color, width, dash, null, null, levels(style.levels()));
        }
        return new AnnotationStyleView(color, width, dash, null, null, null);
    }

    private static BigDecimal width(BigDecimal in, Drawing.Kind kind) {
        BigDecimal fallback = new BigDecimal(switch (kind) {
            case RECT -> "1.4";
            case FIB -> "1.2";
            default -> "1.6";
        });
        if (in == null) {
            return fallback;
        }
        if (in.compareTo(BigDecimal.valueOf(MIN_WIDTH)) < 0) {
            return BigDecimal.valueOf(MIN_WIDTH);
        }
        if (in.compareTo(BigDecimal.valueOf(MAX_WIDTH)) > 0) {
            return BigDecimal.valueOf(MAX_WIDTH);
        }
        return in;
    }

    private static String color(String in, String fallback) {
        return in != null && COLOR.matcher(in.trim()).matches() ? in.trim() : fallback;
    }

    private static BigDecimal fillOpacity(BigDecimal in) {
        if (in == null) {
            return DEFAULT_FILL_OPACITY;
        }
        if (in.compareTo(MIN_FILL_OPACITY) < 0) {
            return MIN_FILL_OPACITY;
        }
        if (in.compareTo(MAX_FILL_OPACITY) > 0) {
            return MAX_FILL_OPACITY;
        }
        return in;
    }

    /**
     * 只认 -1 / 0 / 1 / 2 四个比例，缺的补出厂色，顺序固定 ——
     * 界面上永远不会出现第五条线，也不会因为用户传了个 0.618 就多一条。
     */
    private static List<AnnotationStyleView.FibLevelView> levels(List<AnnotationStyleView.FibLevelView> in) {
        Map<Integer, String> byRatio = new LinkedHashMap<>();
        for (AnnotationStyleView.FibLevelView level : in == null ? List.<AnnotationStyleView.FibLevelView>of() : in) {
            if (level == null || !FIB_RATIOS.contains(level.ratio())) {
                continue;
            }
            byRatio.put(level.ratio(), color(level.color(), FIB_COLORS.get(level.ratio())));
        }
        List<AnnotationStyleView.FibLevelView> out = new ArrayList<>(FIB_RATIOS.size());
        for (Integer ratio : FIB_RATIOS) {
            out.add(new AnnotationStyleView.FibLevelView(ratio, byRatio.getOrDefault(ratio, FIB_COLORS.get(ratio))));
        }
        return out;
    }

    /**
     * 模板名的兜底与截断。
     *
     * <p>截断而不是拒绝：名字超长是手滑，不是错误。空名字用工具名顶上 ——
     * 一条没有名字的 chip 在界面上就是一个看不见的按钮。
     */
    static String name(String in, Drawing.Kind kind) {
        String trimmed = in == null ? "" : in.trim();
        if (trimmed.isEmpty()) {
            return label(kind);
        }
        return trimmed.length() <= MAX_NAME_LENGTH ? trimmed : trimmed.substring(0, MAX_NAME_LENGTH);
    }
}
