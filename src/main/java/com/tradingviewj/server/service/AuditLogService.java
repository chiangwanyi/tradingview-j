package com.tradingviewj.server.service;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.ReplayActionPayload;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.repository.AuditEventRepository;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/**
 * 写一条操作记录（{@code audit_event}）。<strong>全项目唯一写入点。</strong>
 *
 * <h2>为什么必须只有这一处</h2>
 *
 * <p>{@code seq} 是<strong>账户内</strong>连续的偏序：同一根 K 线上画线、挂单、步进，
 * 只有它分得出先后；交易记录又用 {@code [startSeq, endSeq]} 这段区间回放整个过程。
 * 一旦有第二个地方自己算 {@code seq}，断档或重号立刻发生，而这两种错误都<strong>不会当场报错</strong>
 * —— 只会在某次回放时少演一步，或者两步顺序颠倒。
 *
 * <h2>payload 怎么存</h2>
 *
 * <p>存<strong>序列化后的 JSON 文本</strong>，不是直接存对象。原因见 {@link AuditEvent} 的说明：
 * 反序列化时不知道还原成哪个类，只能全存成 {@code Map}。存文本 + 一个类型化的
 * {@code action}，读的一方按 action 自己挑 record 解析。
 *
 * <p>写入本身<strong>不校验</strong>负载：{@link ReplayActionPayload} 那一层刻意只描述结构，
 * 「这个动作该带什么」由调用方负责。要挡的是「缺账户 / 缺标的 / 缺 K 线时间」这类硬错误，
 * 那些在 {@link AuditEvent#of} 里已经挡了。
 */
@Service
@RequiredArgsConstructor
public class AuditLogService {

    private final AuditEventRepository events;
    private final ObjectMapper json;

    /**
     * 追加一条流水并返回它（{@code seq} 已经填好）。
     *
     * @param accountId 算在哪个 PF 账户头上
     * @param symbolId  发生在哪个品种的盘面上
     * @param barTime   当时盘面停在哪根 K 线上
     * @param actor     谁做的（目前只有 {@link Actor#HUMAN} 从这里进来）
     * @param payload   这次动作的负载，不能为 null（没有负载的动作走 {@link AuditEvent#base}）
     */
    @Transactional
    public AuditEvent append(String accountId, String symbolId, String barTime,
                             Actor actor, ReplayActionPayload payload) {
        int seq = events.findTopByAccountIdOrderBySeqDesc(accountId)
                .map(last -> last.getSeq() + 1)
                .orElse(0);
        AuditEvent event = AuditEvent.of(accountId, symbolId, barTime, seq, actor,
                payload.action(), writeJson(payload));
        return events.save(event);
    }

    private String writeJson(ReplayActionPayload payload) {
        try {
            return json.writeValueAsString(payload);
        } catch (JsonProcessingException e) {
            // 序列化失败说明负载类型里有画不出来的东西（循环引用 / 没有 getter 的类）。
            // 这时宁可整条动作失败，也不要写一条 payload 为空的流水 —— 那条流水事后读不回来。
            throw new IllegalStateException("操作记录的负载无法序列化: " + payload.getClass().getSimpleName(), e);
        }
    }
}
