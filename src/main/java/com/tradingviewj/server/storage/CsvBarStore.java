package com.tradingviewj.server.storage;

import com.tradingviewj.server.config.AppProperties;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Component;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.List;
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
 * <p>故意<strong>不做缓存</strong>：一整天的 276 根 K 线读一次是毫秒级，缓存带来的失效问题
 * 比省下的时间值钱得多。
 */
@Slf4j
@Component
public class CsvBarStore {

    /** CSV 表头第一列。用来判断首行是不是表头（Go 版可配是否写表头）。 */
    private static final String HEADER_FIRST_COLUMN = "time";

    private final Path root;

    public CsvBarStore(AppProperties properties) {
        this.root = Path.of(properties.getBarRoot()).toAbsolutePath().normalize();
    }

    /** 某个标的的全部日文件，按文件名升序（= 时间升序）。 */
    public List<Path> dayFiles(String folder) {
        Path dir = resolveFolder(folder);
        if (!Files.isDirectory(dir)) {
            return List.of();
        }
        try (Stream<Path> files = Files.list(dir)) {
            return files.filter(p -> p.getFileName().toString().endsWith(".csv")).sorted().toList();
        } catch (IOException e) {
            throw new UncheckedIOException("读取目录失败: " + dir, e);
        }
    }

    /** 读某一天。文件不存在返回空列表（不是异常：休市日就是没有文件）。 */
    public List<Bar> readDay(String folder, LocalDate date) {
        Path file = resolveFolder(folder).resolve(date + ".csv");
        if (!Files.isRegularFile(file)) {
            return List.of();
        }
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

    /** {@code time,open,high,low,close,volume}；解析不了返回 null（由调用方计数）。 */
    private static Bar parse(String line) {
        String[] parts = line.split(",");
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
