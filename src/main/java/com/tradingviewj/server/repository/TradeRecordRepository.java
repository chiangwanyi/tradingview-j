package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeRecord;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/**
 * 交易记录仓库。
 *
 * <p>注意<strong>没有</strong>「查没有账户的记录」这种方法：{@code account_id} 是
 * {@code NOT NULL}，删账户之后保留的是<strong>原来那个 id</strong>（指向一个不存在的账户），
 * 不是 NULL。曾经有过一个 {@code findByAccountIdIsNull()}，它永远返回空 ——
 * 「删账户后仍能看所有记录」靠的是 {@link #findAllByOrderByEntryBarTimeDesc()}，
 * 不是靠找空账户。
 */
public interface TradeRecordRepository extends JpaRepository<TradeRecord, String> {

    /** 某个 PF 账户下的全部记录（考核明细）：新的在前。 */
    List<TradeRecord> findByAccountIdOrderByCreatedAtDesc(String accountId);

    /** 某个 PF 账户下的全部记录，按开仓时间排（复盘时按时间顺着看）。 */
    List<TradeRecord> findByAccountIdOrderByEntryBarTimeAsc(String accountId);

    /**
     * 某个 PF 账户下<strong>已经平完</strong>的记录，按开仓时间排 —— 交易日历用这个。
     *
     * <p>日历上「点某一天看当天交易」不能混进未平仓的：那笔还没有结束时间，
     * 归到哪一天都不对，而且它的盈亏还是浮动的，列出来只会误导。
     */
    List<TradeRecord> findByAccountIdAndClosedTrueOrderByEntryBarTimeAsc(String accountId);

    /**
     * <strong>全部</strong>记录，按开仓时间倒序 —— 「所有交易记录」页用这个。
     *
     * <p>这条查询<strong>不按账户过滤</strong>，是「删账户之后仍能看见」的落点：
     * 记录里的 {@code accountId} 保留着原值（指向可能已经不存在的账户），
     * 所以点进去仍然能回放，只是归属显示不出来（账户名要另存快照，还没做）。
     *
     * <p>{@code entry_bar_time} 可空（还没开仓的记录），SQLite 排序时 NULL 在前 ——
     * 这类记录本来就该排在最上面，提醒用户「这笔还没结束」。
     */
    List<TradeRecord> findAllByOrderByEntryBarTimeDesc();

    /** 「还在持仓中」的那些（进程崩溃留下的草稿），列表里要单独标出来。 */
    List<TradeRecord> findByClosedFalse();
}
