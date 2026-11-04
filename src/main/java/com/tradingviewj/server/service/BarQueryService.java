package com.tradingviewj.server.service;

import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.BarView;
import com.tradingviewj.server.dto.SymbolSummaryResponse;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.storage.Bar;
import com.tradingviewj.server.storage.CsvBarStore;
import com.tradingviewj.server.storage.SymbolCatalog;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;

import java.nio.file.Path;
import java.time.LocalDate;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * 交易页取数：标的清单 + 一段「上一交易日 → 某个时刻」的 K 线。
 *
 * <h2>为什么单独一个 service，而不是让 Controller 直接读文件</h2>
 *
 * <p>三件事在这里收敛：<strong>code → folder 的映射</strong>（画布传的是标的代码，
 * 磁盘上是目录名）、<strong>「上一个交易日是哪个文件」</strong>（按磁盘上的 CSV 算，不按日历）
 * 和<strong>「没传时刻就用最新一天」</strong>这条默认规则。
 * 放进 Controller 会让每个接口各写一遍，迟早有一处不一致。
 *
 * <h2>只读</h2>
 *
 * <p>整个类<strong>不写任何东西</strong>：不建目录、不落缓存、不碰数据库。
 * 它只是 {@link CsvBarStore} + {@link SymbolCatalog} 的一层查询组装，
 * 和 {@link PropFirmStartPicker} 用同一份数据源，所以「账户起点那天」和
 * 「交易页打开的那天」看到的是同一批 K 线。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class BarQueryService {

    private final CsvBarStore bars;
    private final SymbolCatalog catalog;

    /** 标的清单：清单文件里有什么就返回什么，附带磁盘上数出来的日文件范围。 */
    public List<SymbolSummaryResponse> symbols() {
        List<SymbolSummaryResponse> list = new ArrayList<>();
        for (SymbolSpec spec : catalog.read()) {
            List<Path> files = bars.dayFiles(spec.folder());
            list.add(new SymbolSummaryResponse(
                    spec.code(),
                    spec.folder(),
                    spec.tick(),
                    spec.tickValue(),
                    spec.commission(),
                    files.size(),
                    files.isEmpty() ? null : dayOf(files.get(0)),
                    files.isEmpty() ? null : dayOf(files.get(files.size() - 1))
            ));
        }
        return list;
    }

    /**
     * 一个标的的「上一交易日 + 当天到某个时刻为止」的 K 线。
     *
     * <h2>为什么不是「某一个日文件」</h2>
     *
     * <p>交易页要的是「接着上次的位置继续看盘」：只看当天，图上就没有隔夜的收/开盘、
     * 没有昨晚的走势做参照，画出来是一块没有上下文的图。所以切片的起点定在
     * <strong>上一个交易日的整天开头</strong>，终点定在<strong>调用方给的那一根</strong>。
     *
     * <p>「上一个交易日」<strong>以磁盘上的 CSV 为准</strong>（{@code data/<folder>/<yyyy-MM-dd>.csv}），
     * 而不是按日历减一天：周末、CME 结算休市、抓取程序没跑的那些天本来就没有文件，
     * 按日历减一必然落空。这里取的是<strong>文件名严格小于截止日期的最后一个</strong>。
     *
     * <h2>切点怎么比</h2>
     *
     * <p>时间列是定长明文 {@code yyyy-MM-dd HH:mm}，字典序 = 时间序，所以直接比字符串，
     * 不做时区换算（CSV 存的就是本地明文，转了必然有一天对不上）。
     * 只和「截止那一天的文件」比较 —— 上一交易日是整天取，不参与截断。
     *
     * @param code 标的代码（{@code _symbols.json} 的键）
     * @param time 截止时刻，如 {@code 2025-12-04 09:00}（也接受 {@code yyyy-MM-dd} /
     *             {@code yyyy-MM-ddTHH:mm} / 带秒）；传 null 表示「磁盘上最新的一天整天」
     * @throws BusinessException 标的不存在、时刻格式不对、这个标的没有任何 K 线文件
     */
    public BarSeriesResponse day(String code, String time) {
        SymbolSpec spec = find(code);
        List<Path> files = bars.dayFiles(spec.folder());
        if (files.isEmpty()) {
            throw new BusinessException("标的 " + spec.code() + " 还没有任何 K 线文件（目录 " + spec.folder() + " 是空的）");
        }

        // null = 不传时刻：读磁盘上最新的一天，整天给（含它的上一交易日）
        Cutoff cutoff = time == null || time.isBlank() ? null : parseTime(time);
        LocalDate target = cutoff == null ? dayDateOf(files.get(files.size() - 1)) : cutoff.date();

        List<Bar> raw = new ArrayList<>();
        LocalDate previous = previousTradingDay(files, target);
        if (previous != null) {
            // 上一交易日整天搬过来，不截断：这一段是「隔夜参照」，缺一根就少一根
            raw.addAll(bars.readDay(spec.folder(), previous));
        }
        for (Bar b : bars.readDay(spec.folder(), target)) {
            if (cutoff == null || minuteKey(b.barTime()).compareTo(cutoff.moment()) <= 0) {
                raw.add(b);
            }
        }

        List<BarView> views = new ArrayList<>(raw.size());
        for (Bar b : raw) {
            views.add(new BarView(b.barTime(), b.open(), b.high(), b.low(), b.close(), b.volume()));
        }
        return new BarSeriesResponse(spec.code(), spec.folder(), target.toString(),
                spec.tick(), spec.tickValue(), views);
    }

    /**
     * 文件列表里<strong>严格早于</strong> {@code target} 的最后一天；没有更早的文件时返回 null
     * （从最早那天开始看，就没有「上一交易日」可给）。
     */
    private static LocalDate previousTradingDay(List<Path> files, LocalDate target) {
        LocalDate previous = null;
        for (Path file : files) {
            LocalDate date = dayDateOf(file);
            if (date.isBefore(target) && (previous == null || date.isAfter(previous))) {
                previous = date;
            }
        }
        return previous;
    }

    /** 截止时刻：日期（决定读哪个日文件）+ 归一化到 {@code yyyy-MM-dd HH:mm} 的比较键。 */
    private record Cutoff(LocalDate date, String moment) {
    }

    /** {@code 2025-12-04 09:00} / {@code 2025-12-04T09:00} / {@code 2025-12-04 09:00:00} / {@code 2025-12-04}。 */
    private static final Pattern TIME_PATTERN =
            Pattern.compile("^(\\d{4}-\\d{2}-\\d{2})(?:[ T](\\d{2}):(\\d{2})(?::\\d{2})?)?$");

    private static Cutoff parseTime(String time) {
        String trimmed = time.trim();
        Matcher m = TIME_PATTERN.matcher(trimmed);
        if (!m.matches()) {
            throw new BusinessException("时刻格式应为 yyyy-MM-dd HH:mm（如 2025-12-04 09:00），收到: " + time);
        }
        LocalDate date = parseDay(m.group(1));
        String hh = m.group(2) == null ? "00" : m.group(2);
        String mm = m.group(3) == null ? "00" : m.group(3);
        // 只比到分钟：日文件里的时间列就是 5 分钟的刻度，秒没有意义
        return new Cutoff(date, date + " " + hh + ":" + mm);
    }

    /** K 线时间截到分钟，和 {@link #parseTime} 的比较键同口径。 */
    private static String minuteKey(String barTime) {
        String t = barTime == null ? "" : barTime.trim();
        return t.length() > 16 ? t.substring(0, 16) : t;
    }

    private static LocalDate parseDay(String day) {
        try {
            return LocalDate.parse(day);
        } catch (DateTimeParseException e) {
            throw new BusinessException("日期格式应为 yyyy-MM-dd，收到: " + day);
        }
    }

    private static LocalDate dayDateOf(Path file) {
        return parseDay(dayOf(file));
    }

    /** 找标的；找不到时把「清单里到底有哪些」一并说出来 —— 只报「不存在」等于让人去翻文件。 */
    private SymbolSpec find(String code) {
        if (code == null || code.isBlank()) {
            throw new BusinessException("缺少标的代码（code）");
        }
        List<SymbolSpec> all = catalog.read();
        for (SymbolSpec spec : all) {
            if (spec.code().equals(code)) {
                return spec;
            }
        }
        List<String> known = all.stream().map(SymbolSpec::code).toList();
        throw new BusinessException("不认识的标的: " + code + "（清单里只有: " + known + "）");
    }

    /** {@code .../2024-03-04.csv} → {@code 2024-03-04}。 */
    private static String dayOf(Path file) {
        String name = file.getFileName().toString();
        return name.endsWith(".csv") ? name.substring(0, name.length() - 4) : name;
    }
}
