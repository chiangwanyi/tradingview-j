package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.market.Symbol;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.SymbolRepository;
import com.tradingviewj.server.storage.Bar;
import com.tradingviewj.server.storage.CsvBarStore;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;

import java.nio.file.Path;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ThreadLocalRandom;

/**
 * 给新开的 PF 账户<strong>抽一个起点</strong>：在<strong>账户绑定的那个标的</strong>的
 * 全部日文件里随机挑一份，再定位到那天早上 09:00 的那根 K 线。
 *
 * <h2>为什么抽的是「日文件」而不是「日期」</h2>
 *
 * <p>账户绑定唯一标的（2026-10-30 起），所以「哪一天」理论上已经够定位了。仍然抽「文件」，
 * 是因为<strong>起点那根必须真实存在</strong>：从那份文件里读出来的时间不可能指向一根
 * 不存在的 K 线，而「先抽日期、再去查有没有这根」要处理「那天没数据 / 那根在禁交易窗口」
 * 两种空结果。挑文件自带品种与存在性两件事。
 *
 * <p>在此之前账户不绑标的，抽的是<strong>全部标的</strong>的日文件；现在只剩一份目录，
 * 于是「某个标的抓得多就更容易被抽中」这个偏差自动消失了 —— 每个标的等概率由
 * 「建户时选哪个标的」表达。
 *
 * <h2>为什么是 09:00</h2>
 *
 * <p>用户指定：固定在当天早上九点。不追求「必须正好有一根 09:00 的 K 线」——
 * 取<strong>当天 09:00 及之后的第一根</strong>，于是：
 *
 * <ul>
 *   <li>数据从 07:00 开始的日子（MES / MNQ 就是这样）会得到 09:00 那根；</li>
 *   <li>09:00 恰好休市、或某天数据从更晚才开始，就顺延到之后的第一根，而不是失败；</li>
 *   <li>落在禁交易窗口（04:45–06:00）里的候选直接跳过 —— 那个窗口的交易不属于任何交易日
 *       （{@link PropFirmClock#tradable}）。09:00 本来不落在这个窗口里，
 *       这条判断是防「将来起点时间可配」时把窗口配置漏掉。</li>
 * </ul>
 *
 * <h2>为什么抽不到就报错，而不是退回到「随便给一根」</h2>
 *
 * <p>「没有数据」和「数据都在禁交易窗口里」都是<strong>环境问题</strong>，
 * 不是可以糊过去的业务分支。安静地给一根假的起点，会让后续所有交易日计算、
 * 日历、回放都建立在一个不存在的时间点上，而且当场看不出来。
 */
@Slf4j
@Service
public class PropFirmStartPicker {

    /** 起点定位的目标时刻：当天早上九点。 */
    public static final LocalTime START_TIME = LocalTime.of(9, 0);

    private static final String CSV_SUFFIX = ".csv";

    private final SymbolRepository symbols;
    private final CsvBarStore bars;

    public PropFirmStartPicker(SymbolRepository symbols, CsvBarStore bars) {
        this.symbols = symbols;
        this.bars = bars;
    }

    /**
     * 在指定标的的日文件里抽一个起点。
     *
     * @param symbolCode 账户绑定的标的（{@code symbol} 表的 id，即标的代码）
     * @throws BusinessException 没给标的 / 标的不在库里 / 这个标的没有任何日文件 /
     *                           所有候选日都定位不到可交易的 K 线
     */
    public Pick pick(String symbolCode) {
        if (symbolCode == null || symbolCode.isBlank()) {
            throw new BusinessException("开账户必须指定交易标的（symbolId 不能为空）");
        }
        List<Symbol> all = symbols.findAll();
        if (all.isEmpty()) {
            // 这里单独报，是因为它和「目录里没有 csv」是两件事，而两者的现象一模一样
            // （都是抽不到起点）。真实原因是 symbol 表空着 —— 那样排查方向会完全跑偏。
            throw new BusinessException(
                    "数据库里没有任何标的（symbol 表为空），无法给账户分配起始时间。"
                            + "标的清单来自 data/_symbols.json（由抓取程序维护），服务启动时会自动同步；"
                            + "确认该文件存在且有内容后重启服务。");
        }
        Symbol symbol = all.stream()
                .filter(s -> symbolCode.equals(s.getId()))
                .findFirst()
                .orElseThrow(() -> new BusinessException(
                        "不认识的标的: " + symbolCode + "（symbol 表里只有: "
                                + all.stream().map(Symbol::getId).toList() + "）"));
        return pickFrom(bars, List.of(symbol.getFolder()));
    }

    /**
     * 抽起点的全部逻辑，只依赖 {@link CsvBarStore} 和一组目录名 —— 与数据库无关。
     *
     * <p>拆成静态方法是为了可测：一个临时目录 + 一份三行 CSV 就能把
     * 「随机挑一天、定位到 09:00」这件事钉住，不必起 Spring 上下文。
     */
    static Pick pickFrom(CsvBarStore bars, List<String> folders) {
        List<Path> files = new ArrayList<>();
        for (String folder : folders) {
            files.addAll(bars.dayFiles(folder));
        }
        if (files.isEmpty()) {
            throw new BusinessException(
                    "标的目录下没有任何 .csv（已检查 " + folders.size() + " 个目录：" + folders + "），"
                            + "无法给账户分配起始时间。请先用抓取程序录一段行情。");
        }

        // 随机起点 + 线性探测：抽到不合格的一天就顺延，整圈都没有才报错。
        // 这样「某些日子数据残缺」不会让开账户随机失败，而「全都不合格」仍然当场红。
        int offset = ThreadLocalRandom.current().nextInt(files.size());
        for (int i = 0; i < files.size(); i++) {
            Path file = files.get((offset + i) % files.size());
            Pick pick = locate(bars, file);
            if (pick != null) {
                log.debug("起点抽中 {}/{}", pick.folder(), pick.dayFile());
                return pick;
            }
        }
        throw new BusinessException("所有 " + files.size()
                + " 个 K 线日文件都定位不到可交易的起点（09:00 及之后没有 K 线，或全在禁交易窗口）");
    }

    /**
     * 从一份日文件里定位起点；定位不到返回 {@code null}（由调用方换下一份）。
     *
     * @param file {@code <root>/<folder>/<yyyy-MM-dd>.csv}
     */
    private static Pick locate(CsvBarStore bars, Path file) {
        String folder = file.getParent().getFileName().toString();
        String fileName = file.getFileName().toString();
        LocalDate day;
        try {
            day = LocalDate.parse(fileName.substring(0, fileName.length() - CSV_SUFFIX.length()));
        } catch (RuntimeException e) {
            // 目录里混进了不是「日期.csv」的文件：跳过，不当作数据
            log.warn("跳过文件名不是日期的 K 线文件: {}", file);
            return null;
        }

        List<Bar> dayBars = bars.readDay(folder, day);
        for (Bar bar : dayBars) {
            String barTime = bar.barTime();
            LocalDateTime at;
            try {
                at = PropFirmClock.parse(barTime);
            } catch (IllegalArgumentException e) {
                // 单根坏行不该让开账户失败，但要留下痕迹（CsvBarStore 已经按行计过数）
                log.warn("K 线时间无法解析，跳过: {} @ {}", barTime, file);
                continue;
            }
            if (at.toLocalTime().isBefore(START_TIME)) {
                continue;
            }
            if (!PropFirmClock.tradable(at)) {
                continue;
            }
            return new Pick(folder, barTime, fileName);
        }
        return null;
    }

    /**
     * 抽中的起点。写进 {@code prop_firm_account} 的是 {@link #barTime} 和 {@link #dayFile}，
     * 一起构成起点的<strong>唯一键</strong>。
     *
     * <p>这里<strong>故意不带下标</strong>：下标是「位置」，会被内容变化悄悄改变
     * （文件重抓、补一根、删一根坏行），时间才是标识。
     *
     * @param folder  起点来自哪个标的目录。**不落库** —— 账户上的 {@code symbol_id}
     *                已经回答了这个品种，再存一份只会有两份说法。这里留着是为了日志点名。
     * @param barTime 起点那根 K 线的明文时间，如 {@code 2024-03-04 09:00}
     * @param dayFile 起点所在的日文件名，如 {@code 2024-03-04.csv}（不含目录）
     */
    public record Pick(String folder, String barTime, String dayFile) {
    }
}
