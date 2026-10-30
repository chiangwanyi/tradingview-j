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

/**
 * 交易页取数：标的清单 + 某一天的 K 线。
 *
 * <h2>为什么单独一个 service，而不是让 Controller 直接读文件</h2>
 *
 * <p>两件事在这里收敛：<strong>code → folder 的映射</strong>（画布传的是标的代码，
 * 磁盘上是目录名）和<strong>「没写日期就用最新一天」</strong>这条默认规则。
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
     * 一个标的某一天的 K 线。
     *
     * @param code 标的代码（{@code _symbols.json} 的键）
     * @param day  哪一天；传 null 表示「磁盘上最新的一天」
     * @throws BusinessException 标的不存在、日期格式不对、这个标的没有任何 K 线文件
     */
    public BarSeriesResponse day(String code, String day) {
        SymbolSpec spec = find(code);
        List<Path> files = bars.dayFiles(spec.folder());
        if (files.isEmpty()) {
            throw new BusinessException("标的 " + spec.code() + " 还没有任何 K 线文件（目录 " + spec.folder() + " 是空的）");
        }

        LocalDate date = day == null || day.isBlank() ? LocalDate.parse(dayOf(files.get(files.size() - 1))) : parseDay(day);
        List<Bar> raw = bars.readDay(spec.folder(), date);

        List<BarView> views = new ArrayList<>(raw.size());
        for (Bar b : raw) {
            views.add(new BarView(b.barTime(), b.open(), b.high(), b.low(), b.close(), b.volume()));
        }
        return new BarSeriesResponse(spec.code(), spec.folder(), date.toString(),
                spec.tick(), spec.tickValue(), views);
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

    private static LocalDate parseDay(String day) {
        try {
            return LocalDate.parse(day);
        } catch (DateTimeParseException e) {
            throw new BusinessException("日期格式应为 yyyy-MM-dd，收到: " + day);
        }
    }

    /** {@code .../2024-03-04.csv} → {@code 2024-03-04}。 */
    private static String dayOf(Path file) {
        String name = file.getFileName().toString();
        return name.endsWith(".csv") ? name.substring(0, name.length() - 4) : name;
    }
}
