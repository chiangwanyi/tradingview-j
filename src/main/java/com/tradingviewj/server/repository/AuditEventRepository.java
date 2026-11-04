package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.audit.AuditEvent;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;
import java.util.Optional;

/**
 * 操作流水仓库。<strong>只追加</strong>：这里没有 update，也没有 delete 的业务用途
 * （{@code JpaRepository} 自带的那些方法留着是因为接口继承来的，别在业务里调）。
 *
 * <p>所有查询都<strong>必须带 {@code OrderBySeqAsc}</strong>：{@code seq} 是这个账户内
 * 唯一的偏序，同一根 K 线上画三个图形、挂一张单，只有它分得出先后。
 * 按 id 排等于随机排，按 {@code bar_time} 排会把同一根里的顺序抹平。
 */
public interface AuditEventRepository extends JpaRepository<AuditEvent, String> {

    List<AuditEvent> findByAccountIdOrderBySeqAsc(String accountId);

    /**
     * 账户里最后一条操作。
     *
     * <p>下一条的 seq 就是它的 seq + 1。用「查最大再 +1」而不是数据库自增：
     * seq 是<strong>账户内</strong>的连续编号，跨账户不共用，全局自增做不到这件事。
     */
    Optional<AuditEvent> findTopByAccountIdOrderBySeqDesc(String accountId);

    /**
     * 交易记录的<strong>事件区间</strong>：{@code [from, to]} 闭区间，按 seq 升序。
     *
     * <p>这就是「逐 K 回放」的全部数据来源。{@code TradeRecord.startSeq/endSeq}
     * 直接喂给这两个参数，中间不需要再过滤品种 —— 一段交易记录本来就只属于一个品种
     * （{@code TradeRecord.symbolId}），区间不会跨品种。
     *
     * <p>用的是闭区间而不是半开：起点那条 {@code BASE} 事件本身要参与回放，
     * 终点那条（平仓那一步）也要 —— 差一条就少演一步。
     */
    List<AuditEvent> findByAccountIdAndSeqBetweenOrderBySeqAsc(String accountId, int from, int to);

    /**
     * 「这个账户在这个品种上，盘面停在哪了」—— 取最后一条操作，它的 {@code bar_time}
     * 就是进度。
     *
     * <p><strong>这顶替了原来的 {@code replay_cursor} 表</strong>（2026-10-29 删）。
     * 进度不是一个独立的状态，它就是「最后一次操作停在那一根 K 线」。
     * 中间曾经用一列 {@code cursor}（第几根的下标）表达它，同一天也删了：
     * <strong>下标是位置，时间是标识</strong>。
     *
     * <p>必须按 {@code symbolId} 过滤：一个账户同时做 MES / MNQ / MGC 时，
     * 各品种的进度不同，而 seq 是混着排的。
     */
    Optional<AuditEvent> findTopByAccountIdAndSymbolIdOrderBySeqDesc(String accountId, String symbolId);
}
