package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.storage.SymbolCatalog;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.stereotype.Service;

import java.math.BigDecimal;
import java.util.List;
import java.util.Objects;
import java.util.Optional;

/**
 * 把 {@code data/_symbols.json} 同步进 {@code symbol} 表 —— <strong>服务启动时跑一次</strong>。
 *
 * <h2>为什么这件事必须有</h2>
 *
 * <p>{@code symbol} 表是「{@code data/} 下有哪些标的目录」的登记簿，而
 * {@link PropFirmStartPicker} 抽账户起点时<strong>只按这张表里的 folder 去找 CSV</strong>，
 * 从不枚举 {@code data/} 根目录。表空着的时候，{@code data/} 下有几万个 csv 也一个都看不见，
 * 报出来的却是「data/ 下没有 .csv」—— 这就是这个类存在的原因。
 *
 * <p>在此之前，Spring 侧没有任何代码往 {@code symbol} 表写过东西（只有读的
 * {@code SymbolRepository} 和没人调用的 {@code Symbol.of}），所以表从建库起一直是 0 行。
 *
 * <h2>为什么是「同步」而不是「导入一次」</h2>
 *
 * <p>抓取侧随时可能新增标的、改手续费。每次启动按清单 upsert 一遍，让库跟着文件走，
 * 不需要手工补 SQL。同步是<strong>幂等</strong>的：同一个清单跑两次，第二次只有 updated。
 *
 * <h2>启动期失败不拦启动</h2>
 *
 * <p>清单读不出来是环境问题，但服务本身还能提供只读的复盘功能，没必要整机起不来。
 * 这里把异常吞成一条 {@code ERROR} 日志：表要是因此还空着，开账户时会得到一句
 * 明确的「symbol 表为空」，比服务直接起不来好排查。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class SymbolSyncService implements ApplicationRunner {

    private final SymbolCatalog catalog;
    private final SymbolRepository symbols;

    @Override
    public void run(ApplicationArguments args) {
        try {
            sync();
        } catch (RuntimeException e) {
            log.error("标的清单同步失败，symbol 表可能为空：{}", catalog.file(), e);
        }
    }

    /**
     * 按清单把所有标的比对一遍：没有的新建，有的就地改（含手续费）。
     *
     * <p><strong>不删不在此清单里的标的</strong>：库里有而文件里没有，可能是文件被临时
     * 覆盖了、也可能抓取侧改了写法。删掉会让已有成交引用的标的消失 —— 那属于数据治理，
     * 不该由一次启动顺手完成。这类标的只记一条 warn。
     *
     * @return 这次实际动了多少行
     * @throws com.tradingviewj.server.storage.SymbolCatalog 读文件失败时抛
     *         {@link java.io.UncheckedIOException}
     */
    public Report sync() {
        List<SymbolSpec> specs = catalog.read();
        int added = 0;
        int updated = 0;
        int unchanged = 0;
        int skipped = 0;

        for (SymbolSpec spec : specs) {
            try {
                Optional<Symbol> existing = symbols.findById(spec.code());
                if (existing.isPresent()) {
                    Symbol symbol = existing.get();
                    if (matches(symbol, spec)) {
                        // 没变就不写：否则每次启动 updated_at 全被刷新，
                        // 日志里「更新 4 条」会把真的改了费率的那个淹掉
                        unchanged++;
                        continue;
                    }
                    symbol.setFolder(spec.folder());
                    symbol.setTick(spec.tick());
                    symbol.setTickValue(spec.tickValue());
                    // 手续费是同步的重点：抓取侧改了费率，重启后库里的成交成本跟着变
                    symbol.setCommission(spec.commission());
                    symbols.save(symbol);
                    updated++;
                } else {
                    Symbol symbol = Symbol.of(spec.code(), spec.folder(), spec.commission());
                    symbol.setTick(spec.tick());
                    symbol.setTickValue(spec.tickValue());
                    symbols.save(symbol);
                    added++;
                }
            } catch (RuntimeException e) {
                // 最典型的是 folder 的 UNIQUE 约束：库里已有别的 code 占着同一个目录。
                // 跳过这一条，让剩下的标的照常同步，不要一条坏数据带走整张表。
                log.warn("标的 {} 同步失败，已跳过: folder={} 原因={}",
                        spec.code(), spec.folder(), e.getMessage());
                skipped++;
            }
        }

        log.info("标的清单同步完成（{}）：新增 {}，更新 {}，未变 {}，跳过 {}",
                catalog.file(), added, updated, unchanged, skipped);
        warnAboutLeftovers(specs);
        return new Report(added, updated, unchanged, skipped);
    }

    /** 库里的这一行和清单里的这一条是否等价（金额按数值比，{@code 1} 与 {@code 1.0} 视为相同）。 */
    private static boolean matches(Symbol symbol, SymbolSpec spec) {
        return Objects.equals(symbol.getFolder(), spec.folder())
                && sameNumber(symbol.getTick(), spec.tick())
                && sameNumber(symbol.getTickValue(), spec.tickValue())
                && sameNumber(symbol.getCommission(), spec.commission());
    }

    private static boolean sameNumber(BigDecimal left, BigDecimal right) {
        if (left == null || right == null) {
            return left == right;
        }
        return left.compareTo(right) == 0;
    }

    /** 库里还有、但清单里已经没有的标的：只提醒，不删。 */
    private void warnAboutLeftovers(List<SymbolSpec> specs) {
        List<String> inFile = specs.stream().map(SymbolSpec::code).toList();
        List<String> leftovers = symbols.findAll().stream()
                .map(Symbol::getId)
                .filter(code -> !inFile.contains(code))
                .toList();
        if (!leftovers.isEmpty()) {
            log.warn("以下标的在库里存在、但清单里没有（没有删除，只是提醒）: {}", leftovers);
        }
    }

    /**
     * 一次同步的结果，四种计数之和 = 清单里被采纳的标的条数。
     *
     * @param added     库里原本没有、新建的
     * @param updated   库里已有、内容确实变了被改写的（含改了手续费）
     * @param unchanged 库里已有、和清单一致因而没写的
     * @param skipped   因为冲突没能落库的（日志里点名）
     */
    public record Report(int added, int updated, int unchanged, int skipped) {

        public boolean changedAnything() {
            return added > 0 || updated > 0;
        }
    }
}
