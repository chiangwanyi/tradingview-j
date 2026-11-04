package com.tradingviewj.server.storage;

import com.tradingviewj.server.config.AppProperties;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Component;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.BasicFileAttributes;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.stream.Stream;

/**
 * K 线仓库：{@code <root>/<folder>/<yyyy-MM-dd>.csv}。
 *
 * <p><strong>这是本项目唯一「不进数据库」的数据</strong>，而且是有意为之：
 *
 * <ul>
 *   <li>一天一个文件、一个标的几百个文件，进库只会多一层索引和一层事务，查询并不会更快；</li>
 *   <li>CSV 是人类可读的，出问题时能用编辑器打开、能用 diff 看变化 —— 行情数据值这个属性；</li>
 *   <li>抓取侧（Go 程序）已经在往里写，两边共用一种格式比两边各写一份库要稳。</li>
 * </ul>
 *
 * <p><strong>职责边界</strong>：它只做「按目录 + 日期读出一串 {@link Bar}」，不写、不合并、
 * 不判断缺口。写入是抓取侧的事；统计和缺口分析是 service 的事。
 *
 * <p><strong>缓存策略</strong>（2026-11-03 加）：早期的版本每次都重新读盘、重新解析，理由是
 * 「一天的 276 根读一次是毫秒级」。这个估算在<strong>单次点【下一根】</strong>时成立，但在
 * 「每次推进都把整段历史重放一遍」的路径上不成立 —— 机器人压测 480 步里，读盘 + 解析占了
 * 单步的四成。所以现在加两层<strong>有界</strong>缓存：
 *
 * <ul>
 *   <li><strong>日文件</strong>：按 {@code (folder, 日期)} 缓存解析结果，失效条件是
 *       <strong>mtime 或 size 变了</strong> —— 抓取程序追加写一定会改 size，重写一定会改 mtime，
 *       两种变化都逃不掉。上限 {@value #MAX_CACHED_DAYS} 天，LRU 淘汰。</li>
 *   <li><strong>目录列表</strong>：按 folder 缓存文件名列表，失效条件是<strong>目录 mtime 变了
 *       或超过 {@value #LISTING_TTL_MS} 毫秒</strong>（目录 mtime 在 Windows 上只在增删文件时变，
 *       加 TTL 是给「同一秒内新建」兜底）。列表本身不含内容，所以不占内存。</li>
 * </ul>
 *
 * <p>缓存<strong>不改变可见性语义</strong>：外部抓取程序写进来的新 K 线，
 * 最迟 {@value #LISTING_TTL_MS} 毫秒后就能读到；日文件内容变化则立刻失效。
 * 返回的 {@code List} 是<strong>不可变</strong>的，调用方改不了缓存里的东西。
 */
@Slf4j
@Component
public class CsvBarStore {

    /** CSV 表头第一列。用来判断首行是不是表头（Go 版可配是否写表头）。 */
    private static final String HEADER_FIRST_COLUMN = "time";

    /** {@link #lastBarTime} 从文件尾部读多少字节 —— 够装几十行，且不会因为文件变大而变慢。 */
    private static final int TAIL_BYTES = 4096;

    /** 日文件缓存最多留几天。一天 276 根 × 6 个 BigDecimal，64 天约十几 MB。 */
    private static final int MAX_CACHED_DAYS = 64;

    /** 目录列表缓存的最长寿命：超过就重新列一次，给「同一秒内新建文件」兜底。 */
    private static final long LISTING_TTL_MS = 2000;

    /** 解析好的一天。{@code modified}/{@code size} 是判失效的全部依据。 */
    private record CachedDay(long modified, long size, List<Bar> bars) {
    }

    /** 一个目录里的文件名列表。{@code takenAt} 是读目录的时刻，用来算 TTL。 */
    private record CachedListing(long modified, long takenAt, List<Path> files) {
    }

    /** 一间日文件「最后一根」的读数（{@link #lastBarTime}）。 */
    private record CachedTail(long modified, long size, Optional<String> value) {
    }

    private final Path root;

    /**
     * 日文件缓存，LRU（{@code accessOrder = true} 让「刚用过」的排在尾部）。
     * 加锁访问：请求线程不止一条（HTTP 读接口和 WS 是两个线程），{@code LinkedHashMap} 不是线程安全的。
     */
    private final Map<String, CachedDay> days = new LinkedHashMap<>(128, 0.75f, true) {
        @Override
        protected boolean removeEldestEntry(Map.Entry<String, CachedDay> eldest) {
            return size() > MAX_CACHED_DAYS;
        }
    };

    /** 目录列表缓存。按 folder 索引，条目数 = 标的数，很小。 */
    private final Map<String, CachedListing> listings = new ConcurrentHashMap<>();

    /** 「最后一根」缓存，同样是 LRU：数交易日会一口气问几百天，全留着没意义。 */
    private final Map<String, CachedTail> tails = new LinkedHashMap<>(128, 0.75f, true) {
        @Override
        protected boolean removeEldestEntry(Map.Entry<String, CachedTail> eldest) {
            return size() > 256;
        }
    };

    public CsvBarStore(AppProperties properties) {
        this.root = Path.of(properties.getBarRoot()).toAbsolutePath().normalize();
    }

    /** 某个标的的全部日文件，按文件名升序（= 时间升序）。 */
    public List<Path> dayFiles(String folder) {
        Path dir = resolveFolder(folder);
        BasicFileAttributes dirAttrs = attributes(dir);
        if (dirAttrs == null || !dirAttrs.isDirectory()) {
            listings.remove(folder);
            return List.of();
        }
        long now = System.currentTimeMillis();
        CachedListing cached = listings.get(folder);
        if (cached != null
                && cached.modified() == dirAttrs.lastModifiedTime().toMillis()
                && now - cached.takenAt() < LISTING_TTL_MS) {
            return cached.files();
        }
        List<Path> files;
        try (Stream<Path> stream = Files.list(dir)) {
            files = stream.filter(p -> p.getFileName().toString().endsWith(".csv")).sorted().toList();
        } catch (IOException e) {
            throw new UncheckedIOException("读取目录失败: " + dir, e);
        }
        listings.put(folder, new CachedListing(dirAttrs.lastModifiedTime().toMillis(), now, files));
        return files;
    }

    /** 读某一天。文件不存在返回空列表（不是异常：休市日就是没有文件）。 */
    public List<Bar> readDay(String folder, LocalDate date) {
        Path file = resolveFolder(folder).resolve(date + ".csv");
        BasicFileAttributes attrs = attributes(file);
        if (attrs == null || !attrs.isRegularFile()) {
            synchronized (days) {
                days.remove(cacheKey(folder, date));
            }
            return List.of();
        }
        long modified = attrs.lastModifiedTime().toMillis();
        long size = attrs.size();
        String key = cacheKey(folder, date);
        synchronized (days) {
            CachedDay cached = days.get(key);
            if (cached != null && cached.modified() == modified && cached.size() == size) {
                return cached.bars();
            }
        }
        List<Bar> parsed = parseFile(file);
        List<Bar> immutable = List.copyOf(parsed);
        synchronized (days) {
            days.put(key, new CachedDay(modified, size, immutable));
        }
        return immutable;
    }

    private static String cacheKey(String folder, LocalDate date) {
        return folder + '/' + date;
    }

    /** 文件属性；不存在或读不到时返回 null（调用方按「没有这个文件」处理）。 */
    private static BasicFileAttributes attributes(Path path) {
        try {
            return Files.readAttributes(path, BasicFileAttributes.class);
        } catch (IOException e) {
            return null;
        }
    }

    private List<Bar> parseFile(Path file) {
        List<Bar> bars = new ArrayList<>();
        int bad = 0;
        try (BufferedReader reader = Files.newBufferedReader(file, StandardCharsets.UTF_8)) {
            String line;
            boolean first = true;
            while ((line = reader.readLine()) != null) {
                if (line.isBlank()) {
                    continue;
                }
                if (first) {
                    first = false;
                    if (line.regionMatches(true, 0, HEADER_FIRST_COLUMN, 0, HEADER_FIRST_COLUMN.length())) {
                        continue; // 表头
                    }
                }
                Bar bar = parse(line);
                if (bar == null) {
                    bad++;
                } else {
                    bars.add(bar);
                }
            }
        } catch (IOException e) {
            throw new UncheckedIOException("读取 K 线文件失败: " + file, e);
        }
        if (bad > 0) {
            // 坏行要报出来，不能悄悄跳过：跳过的行数和「今天怎么少了两根」是同一个问题
            log.warn("K 线文件 {} 有 {} 行无法解析，已跳过", file, bad);
        }
        return bars;
    }

    /**
     * 这一天<strong>最后一根</strong> K 线的时间明文（{@code yyyy-MM-dd HH:mm}）；没有文件或空文件返回 empty。
     *
     * <p>只用来回答一个问题：<strong>这一天有没有属于它自己的交易日</strong>
     * （见 {@code TradingCalendar}）。本项目的日文件是<strong>日历日</strong>切的
     * （{@code 2024-04-19.csv} 装的是 04-19 00:00–23:55），而交易日 D 是
     * {@code [D 06:00, D+1 04:00)} —— 于是周六那份文件里只有周五那场 00:00–04:55 的尾巴，
     * 它<b>不是</b>一个交易日。判据就是「当天最后一根在 06:00 之后」：
     * 有自己那一场的日子，文件里一定写到 23:55（半日市也写到午后）；只有尾巴的日子，
     * 最后一根停在 04:55 之前。
     *
     * <p><strong>不读整天</strong>：只 seek 到文件尾部读最后几 KB —— 数一个账户走过的交易日
     * 要对区间内每一天问一次，整天读会把「打开首页」变成几百次全文件解析。
     */
    public Optional<String> lastBarTime(String folder, LocalDate date) {
        Path file = resolveFolder(folder).resolve(date + ".csv");
        BasicFileAttributes attrs = attributes(file);
        if (attrs == null || !attrs.isRegularFile()) {
            return Optional.empty();
        }
        String key = cacheKey(folder, date);
        long modified = attrs.lastModifiedTime().toMillis();
        long size = attrs.size();
        synchronized (tails) {
            CachedTail cached = tails.get(key);
            if (cached != null && cached.modified() == modified && cached.size() == size) {
                return cached.value();
            }
        }
        Optional<String> tail = readTail(file);
        synchronized (tails) {
            tails.put(key, new CachedTail(modified, size, tail));
        }
        return tail;
    }

    /** 真的去文件尾部读那几 KB。见 {@link #lastBarTime} 的说明。 */
    private Optional<String> readTail(Path file) {
        try (RandomAccessFile raf = new RandomAccessFile(file.toFile(), "r")) {
            long length = raf.length();
            if (length == 0) {
                return Optional.empty();
            }
            int size = (int) Math.min(TAIL_BYTES, length);
            byte[] buf = new byte[size];
            raf.seek(length - size);
            raf.readFully(buf);
            String[] lines = new String(buf, StandardCharsets.UTF_8).split("\\R");
            for (int i = lines.length - 1; i >= 0; i--) {
                String line = lines[i].trim();
                if (line.isEmpty()) {
                    continue;
                }
                int comma = line.indexOf(',');
                String time = (comma > 0 ? line.substring(0, comma) : line).trim();
                // 文件太小、尾部那段从半行开始时，这条会是残行；格式不对就继续往上找
                if (time.length() >= 16 && time.charAt(4) == '-' && time.charAt(10) == ' ') {
                    return Optional.of(time.substring(0, 16));
                }
            }
            return Optional.empty();
        } catch (IOException e) {
            throw new UncheckedIOException("读取 K 线文件尾部失败: " + file, e);
        }
    }

    /** 这一天有没有 K 线文件（休市日没有文件，这是正常状态而不是错误）。 */
    public boolean hasDay(String folder, LocalDate date) {
        return Files.isRegularFile(resolveFolder(folder).resolve(date + ".csv"));
    }

    /** {@code 2024-03-04.csv → 2024-03-04}；名字不像日期时返回 {@code null}。 */
    public static LocalDate dayOf(Path file) {
        String name = file.getFileName().toString();
        if (name.endsWith(".csv")) {
            name = name.substring(0, name.length() - 4);
        }
        try {
            return LocalDate.parse(name);
        } catch (RuntimeException e) {
            return null;
        }
    }

    /**
     * 一串<strong>按文件名升序</strong>的日文件里，第一个「日期 ≥ {@code day}」的下标。
     *
     * <p>文件名是定长 {@code yyyy-MM-dd}，所以这个列表天然按日期有序 —— 「从头找到今天」
     * 可以二分，不必把三千多个文件名逐个 {@code LocalDate.parse} 一遍。
     * 名字不像日期的条目按「不早于」处理（起点最多提前，不会漏）。
     *
     * @return 全都早于 {@code day} 时返回 {@code files.size()}
     */
    public static int firstDayOnOrAfter(List<Path> files, LocalDate day) {
        int lo = 0;
        int hi = files.size();
        while (lo < hi) {
            int mid = (lo + hi) >>> 1;
            LocalDate at = dayOf(files.get(mid));
            if (at != null && at.isBefore(day)) {
                lo = mid + 1;
            } else {
                hi = mid;
            }
        }
        return lo;
    }

    /** {@code time,open,high,low,close,volume}；解析不了返回 null（由调用方计数）。 */
    private static Bar parse(String line) {        String[] parts = line.split(",");
        if (parts.length < 5) {
            return null;
        }
        try {
            return new Bar(
                    parts[0].trim(),
                    new BigDecimal(parts[1].trim()),
                    new BigDecimal(parts[2].trim()),
                    new BigDecimal(parts[3].trim()),
                    new BigDecimal(parts[4].trim()),
                    parts.length > 5 && !parts[5].isBlank() ? new BigDecimal(parts[5].trim()) : BigDecimal.ZERO);
        } catch (NumberFormatException e) {
            return null;
        }
    }

    /**
     * 目录名来自标的，必须挡掉 {@code ..} 和绝对路径 —— 否则一个恶意/手误的
     * folder 值就能读到数据目录之外的任意 CSV。
     */
    private Path resolveFolder(String folder) {
        if (folder == null || folder.isBlank()) {
            throw new IllegalArgumentException("标的分目录不能为空");
        }
        Path dir = root.resolve(folder).normalize();
        if (!dir.startsWith(root)) {
            throw new IllegalArgumentException("非法的标的分目录: " + folder);
        }
        return dir;
    }
}
