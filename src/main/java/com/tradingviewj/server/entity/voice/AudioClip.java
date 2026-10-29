package com.tradingviewj.server.entity.voice;

import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 一段语音讲解，挂在某根 K 线上。
 *
 * <p><strong>职责边界</strong>：它只管「谁在哪根 K 线上讲了几秒」，音频本体在磁盘上
 * （{@code <audioRoot>/<accountId>/voice-N.wav}），这里只存<strong>相对路径</strong>。
 * 存绝对路径的后果是换个盘符/换个用户名整个库就废了。
 *
 * <p>为什么语音是<strong>账户下的一级实体</strong>而不是交易记录的子项：
 * 「只想在这根 K 线上讲两句」的人不该被迫先开一段录像。它和图形、挂单同级，
 * 交易记录只是它的一个下游消费者（录制期间录的语音同时被记进那笔记录的步骤里）。
 * 删掉账户<strong>不会</strong>带走语音：{@code account_id} 原样保留，元数据与 WAV 都还在。
 *
 * <p>额度是「<strong>同一根 K 线最多 9 段</strong>」，按 K 线时间数。
 * 整块盘面的语音总数不设上限 —— 这条限制的唯一用处是防止用户在同一根上反复重录。
 * 校验放在 service，实体本身不做 —— 需要知道「已经是第几段」才能判，那是仓储的活。
 */
@Getter
@Entity
@Table(name = "audio_clip")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AudioClip extends BaseEntity {

    /**
     * 属于哪个 PF 账户。
     *
     * <p><strong>NOT NULL 且不加外键</strong>：账户被删掉之后这里仍然保留原来那个 id，
     * 语音元数据与 WAV 文件（本来就在磁盘上）都原样保留。
     */
    @Column(nullable = false)
    private String accountId;

    /** 挂在哪根 K 线上。 */
    @Column(nullable = false)
    private String barTime;

    /** 同一根上的第几段（从 0 起）。 */
    @Column(nullable = false)
    private int seq;

    /** 相对路径，例如 {@code <accountId>/voice-3.wav}。 */
    @Column(nullable = false)
    private String fileName;

    @Column(nullable = false)
    private int durationMs;

    @Column(nullable = false)
    private long sizeBytes;

    /** 谁录的。多人共用一块盘面时要能看出来。 */
    @Enumerated(EnumType.STRING)
    @Column
    private Actor actor;

    /**
     * 录一段语音。
     *
     * @param accountId 录在哪个 PF 账户里，<strong>不能为空</strong>：语音是考核期里的操作
     * @throws IllegalArgumentException 没给账户
     */
    public static AudioClip record(String accountId, String barTime, int seq, String fileName,
                                   int durationMs, long sizeBytes, Actor actor) {
        if (accountId == null || accountId.isBlank()) {
            throw new IllegalArgumentException("语音必须录在某个 PF 账户里，收到: " + accountId);
        }
        AudioClip clip = new AudioClip();
        clip.accountId = accountId;
        clip.barTime = barTime;
        clip.seq = seq;
        clip.fileName = fileName;
        clip.durationMs = durationMs;
        clip.sizeBytes = sizeBytes;
        clip.actor = actor;
        return clip;
    }
}
