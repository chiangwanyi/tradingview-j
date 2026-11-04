package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.record.TradeRecord;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.data.jpa.repository.Modifying;
import org.springframework.data.jpa.repository.Query;

import java.util.List;
import java.util.Optional;

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

    /**
     * 这个账户<strong>当前那条未闭合</strong>的记录。
     *
     * <p>一个账户同一时刻最多一条：账户绑定唯一标的，净头寸也就只有一个符号。
     * 反手的「闭合旧记录 + 开新记录」在同一个事务里完成，中间不会出现两条。
     * 数据库侧由 {@code ux_record_open}（{@code WHERE closed = 0} 的 partial unique index）
     * 兜住 —— 这条查询和那条索引是同一件事的两面，别只留一个。
     *
     * <p>返回 {@link java.util.Optional} 而不是 List：调用方要的是「有没有」，
     * 拿到 List 还得自己判长度、自己挑一个，那正好是「两条都保留，行为随机」的来源。
     */
    Optional<TradeRecord> findFirstByAccountIdAndClosedFalseOrderByStartSeqAsc(String accountId);

    /**
     * 清空全部「记录 ↔ 标签」关联行（{@code trade_record_tag}），返回删掉几行。
     *
     * <p><strong>只删关联，不删标签本身</strong>：标签字典在 {@code trade_tag}
     * （{@link com.tradingviewj.server.entity.record.TradeTag}），是用户自己攒的本子，
     * 交易清空了它该留着。这条 SQL 里出现 {@code trade_record_tag} 就从没碰过 {@code trade_tag}。
     *
     * <p><strong>为什么是原生 SQL，而不是走 {@code @ManyToMany}</strong>：
     * 清空整张关联表在 JPA 里没有批量写法 —— 只能把每条记录加载出来、逐个
     * {@code getTags().clear()}，几万行时那叫「用 ORM 把一次 DELETE 变成 N 次查询」。
     *
     * <p><strong>为什么不靠 {@code ON DELETE CASCADE} 顺手删掉</strong>：
     * schema 里 {@code trade_record_tag.record_id} 确实写了级联，删记录时关联行也会跟着走。
     * 但那样一来「这次到底删了什么、删了多少」就成了数据库的隐式行为，service 拿不到行数。
     * 清空是不可逆操作，账要对得上，所以这里显式删一次（<strong>必须在删记录之前</strong>）。
     */
    @Modifying
    @Query(value = "DELETE FROM trade_record_tag", nativeQuery = true)
    int deleteAllTags();
}
