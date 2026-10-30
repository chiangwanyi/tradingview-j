package com.tradingviewj.server.entity.replay;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 复盘进度：**用户看到第几根 K 线**。
 *
 * <p>这是整个系统里唯一一种「算不出来、必须存」的状态。余额能从成交重放、
 * 持仓能从成交重放、盈利日能从 K 线重放，但「我今天推到哪一根了」没有任何别的数据推得出来 ——
 * 关掉程序再打开就得从这一根接着看。
 *
 * <p>一个账户一个品种一行（{@code (account_id, symbol_id)} 唯一）。为什么不挂在账户上：
 * 一个账户同时做 MES / MNQ / MGC，每个品种的进度不同；加列要加 N 列，存 JSON 又没法按品种查。
 *
 * <p>指向的是<strong>位置</strong>而不是内容：{@code bar_time} + {@code bar_index} 定位到那根 K 线，
 * 不存它的价格。行情文件后来被重新抓取、那根 K 线的高低价变了，进度依然有效。
 */
@Getter
@Entity
@Table(name = "replay_cursor")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class ReplayCursor extends BaseEntity {

    /** 哪个账户的进度。NOT NULL 且不加外键：账户删了这一行原样保留。 */
    @Column(nullable = false, updatable = false)
    private String accountId;

    /** 哪个品种的进度。 */
    @Column(nullable = false, updatable = false)
    private String symbolId;

    /** 已经看到的最后一根 K 线（本地时区明文，和别处一致）。 */
    @Column(nullable = false)
    private String barTime;

    /** 这一根在当天文件里的下标，0 起。 */
    @Column(nullable = false)
    private int barIndex;

    /** 当时读的是哪一天的 CSV（文件名，不含目录）。换日靠它，不用从 barTime 前 10 位猜。 */
    @Column(nullable = false)
    private String dayFile;

    /**
     * 开一条进度：停在某一根上。
     *
     * @param accountId 哪个账户，<strong>不能为空</strong>
     * @param symbolId  哪个品种，<strong>不能为空</strong>
     * @param barTime   停在哪一根
     * @param barIndex  当天文件内的下标，<strong>不能为负</strong>
     * @param dayFile   哪一天的 CSV
     * @throws IllegalArgumentException 缺账户 / 品种 / 文件名，或下标为负
     */
    public static ReplayCursor start(String accountId, String symbolId, String barTime,
                                     int barIndex, String dayFile) {
        requireText(accountId, "复盘进度必须属于某个 PF 账户");
        requireText(symbolId, "复盘进度必须指定标的");
        requireText(dayFile, "复盘进度必须记着读的是哪一天的 K 线文件");
        if (barIndex < 0) {
            throw new IllegalArgumentException("K 线下标不能为负，收到: " + barIndex);
        }
        ReplayCursor cursor = new ReplayCursor();
        cursor.accountId = accountId;
        cursor.symbolId = symbolId;
        cursor.barTime = barTime;
        cursor.barIndex = barIndex;
        cursor.dayFile = dayFile;
        return cursor;
    }

    /**
     * 往前挪（或跳到别处）一根。
     *
     * <p>不校验「只能往前走」：用户点「回到当天开盘」是合法操作，进度条本来就该能拖回去。
     * 账务不受影响 —— 余额永远按成交重放，跟游标停在哪根无关。
     */
    public void moveTo(String barTime, int barIndex, String dayFile) {
        requireText(dayFile, "复盘进度必须记着读的是哪一天的 K 线文件");
        if (barIndex < 0) {
            throw new IllegalArgumentException("K 线下标不能为负，收到: " + barIndex);
        }
        this.barTime = barTime;
        this.barIndex = barIndex;
        this.dayFile = dayFile;
    }

    /** 这一根属于哪一天（{@code day_file} 去掉扩展名），换日时用来和下一份文件比对。 */
    public String day() {
        int dot = dayFile.lastIndexOf('.');
        return dot < 0 ? dayFile : dayFile.substring(0, dot);
    }

    private static void requireText(String value, String message) {
        if (value == null || value.isBlank()) {
            throw new IllegalArgumentException(message + "，收到: " + value);
        }
    }
}
