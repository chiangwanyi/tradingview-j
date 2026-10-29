package com.tradingviewj.server.entity.report.summary;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.tradingviewj.server.entity.store.scan.WeekExpect;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「这份快照是在什么口径下扫出来的」（对应 Go 的 {@code webreport.Meta}）。
 *
 * <p>网页要把这些显示出来：同一个数据目录，时区或周期一变，「哪一天」和应有根数就会跟着变。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Meta {

    /** 数据目录（绝对路径） */
    private String root;

    /** 一根 K 线多少秒（配置的 intervalSeconds） */
    private long step;

    /** 判「哪一天」用的时区 */
    private String tz;

    private boolean exists;

    private String version;

    /** 本次扫描完成时刻（Unix 秒） */
    private long generatedAt;

    /**
     * 快照修订号（生成时刻的 Unix 毫秒）。
     * 页面靠「修订号有没有变」决定要不要重绘：抓取过程中一秒里完全可能重扫两次。
     */
    private long rev;

    /** 页面自动同步的间隔（毫秒）；0 = 不自动同步。由服务端给出 */
    private int pollMs;

    /** 本次扫描耗时（毫秒） */
    private long scanMs;

    /** 「这个星期几应当有多少根」的判定表，原样透传，判据只在 store 一处 */
    private List<WeekExpect> expectRule;
}
