package com.tradingviewj.server.service;

import com.tradingviewj.server.storage.Bar;
import com.tradingviewj.server.storage.CsvBarStore;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

import java.nio.file.Path;
import java.time.LocalDate;
import java.time.format.DateTimeParseException;
import java.util.List;

/**
 * 「当前这根 K 线之后是哪一根」——【下一根】的全部依据。
 *
 * <h2>为什么单独一个类</h2>
 *
 * <p>这个问题看着只有一句话，但它有<strong>三个必须一致的口径</strong>，散在各处早晚会分叉：
 *
 * <ol>
 *   <li><strong>往后找会跨文件。</strong> 一天的 CSV 读完了不代表没有下一根 ——
 *       下一个交易日还有。只查「当前这个日文件」的实现会在每天收盘后卡住，
 *       而盘面明明还能往后走。</li>
 *   <li><strong>「当前这一天」可能是空的。</strong> 账户停在 {@code 2026-10-20}，
 *       那天文件被清理了 / 那天休市没抓。这时要<strong>继续往后</strong>找而不是报错，
 *       否则一次误删就把账户永远钉死在一个不存在的位置上。</li>
 *   <li><strong>时间字符串就是顺序。</strong> CSV 的时间列是定长明文
 *       {@code yyyy-MM-dd HH:mm}，字典序 = 时间序。转成 {@code Instant} 再比会引入时区选择，
 *       而 CSV 存的是本地明文，转了必然有一天对不上（和 {@code Bar} 那一段同一个理由）。</li>
 * </ol>
 *
 * <p><strong>只读</strong>：不写文件、不落库、不推进任何游标。推进账户的
 * {@code lastBarTime} 是调用方（{@code TradeDeskService}）的事 —— 这个类只回答「下一根是哪根」。
 */
@Service
@RequiredArgsConstructor
public class BarCursorService {

    private final CsvBarStore bars;

    /**
     * 找到的一根，以及它前后的位置信息。
     *
     * @param bar         那根 K 线
     * @param indexInDay  它是这个日文件里的第几根（从 0 起）。<strong>只在通信负载里用</strong>，
     *                    不落库 —— 下标是位置，文件重抓就会指向另一根（见 README 的「时间才是标识」）
     * @param restOfDay   它之后这个日文件里还剩几根（用来显示「今天还有 N 根」）
     * @param hasNext     它之后<strong>磁盘上</strong>还有没有下一根（跨日也算）
     */
    public record NextBar(Bar bar, int indexInDay, int restOfDay, boolean hasNext) {
    }

    /**
     * {@code from} 之后的第一根。
     *
     * @param folder     标的的数据目录名（{@code _symbols.json} 里的 folder，不是代码）
     * @param from       当前停在哪一根（{@code yyyy-MM-dd HH:mm}）；null = 从头开始
     * @return 没有下一根时返回 null（不是异常：走到磁盘上最后一根是正常状态）
     */
    public NextBar next(String folder, String from) {
        List<Path> files = bars.dayFiles(folder);
        if (files.isEmpty()) {
            return null;
        }
        LocalDate fromDay = dayOf(from);
        for (int f = 0; f < files.size(); f++) {
            Path file = files.get(f);
            LocalDate day = dayDateOf(file);
            // 早于 from 那天的文件整个跳过；from 那天本身要看（当天可能还没走完）
            if (fromDay != null && day.isBefore(fromDay)) {
                continue;
            }
            List<Bar> dayBars = bars.readDay(folder, day);
            for (int i = 0; i < dayBars.size(); i++) {
                Bar bar = dayBars.get(i);
                if (from != null && bar.barTime().compareTo(from) <= 0) {
                    continue;
                }
                int rest = dayBars.size() - i - 1;
                return new NextBar(bar, i, rest, rest > 0 || f < files.size() - 1);
            }
        }
        return null;
    }

    /** 磁盘上还有没有下一根。 */
    public boolean hasNext(String folder, String from) {
        return next(folder, from) != null;
    }

    /** {@code "2026-10-30 09:35"} → {@code 2026-10-30}；格式不认识返回 null（当作「从头找」）。 */
    private static LocalDate dayOf(String barTime) {
        if (barTime == null || barTime.length() < 10) {
            return null;
        }
        try {
            return LocalDate.parse(barTime.substring(0, 10));
        } catch (DateTimeParseException e) {
            return null;
        }
    }

    /** {@code .../2024-03-04.csv} → {@code 2024-03-04}。 */
    private static LocalDate dayDateOf(Path file) {
        String name = file.getFileName().toString();
        return LocalDate.parse(name.endsWith(".csv") ? name.substring(0, name.length() - 4) : name);
    }
}
