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
 * （{@code <audioRoot>/<标的目录>/<日期>/…wav}），这里只存<strong>相对路径</strong>。
 * 存绝对路径的后果是换个盘符/换个用户名整个库就废了。
 *
 * <p>为什么语音是<strong>挂在 K 线上的一级实体</strong>而不是交易记录的子项：
 * 「只想在这根 K 线上讲两句」的人不该被迫先开一段录像。它和图形同级（都只认 K 线），
 * 交易记录只是它的一个下游消费者（录制期间录的语音同时被记进那笔记录的步骤里）。
 *
 * <p><strong>它没有账户</strong>：语音不参与余额、回撤、一致性、盈利日里的任何一项统计，
 * 所以不存 {@code accountId}。记着「这段语音属于哪个账户」只会让同一根 K 线上的讲解
 * 换个账户就听不到了 —— 而它本来就是讲给这根 K 线听的。
 *
 * <p>额度是「<strong>同一根 K 线最多 9 段</strong>」，按（品种, K 线时间）数。
 * 整块盘面的语音总数不设上限 —— 这条限制的唯一用处是防止用户在同一根上反复重录。
 * 校验放在 service，实体本身不做 —— 需要知道「已经是第几段」才能判，那是仓储的活。
 */
@Getter
@Entity
@Table(name = "audio_clip")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class AudioClip extends BaseEntity {

    /** 讲的是哪个品种的 K 线。{@link #barTime} 只说明"哪一根"，得先知道是哪张图的 K 线。 */
    @Column(nullable = false)
    private String symbolId;

    /** 挂在哪根 K 线上。 */
    @Column(nullable = false)
    private String barTime;

    /** 同一根上的第几段（从 0 起）。 */
    @Column(nullable = false)
    private int seq;

    /**
     * 相对路径，按「标的目录 / 日期 / 段」分层，例如
     * {@code CME_MINI_MES1!/2026-10-16/12-00-0.wav}。
     *
     * <p>从前是 {@code <accountId>/voice-N.wav}：账户一删，那层目录就没人负责了，
     * 而语音本来讲的是标的的一根 K 线，跟账户没关系。目录名用标的名（等同
     * {@code data/} 下的文件夹名），复盘时和 CSV 目录长得一样好找。
     */
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
     * @param symbolId 录在哪个品种的 K 线上，<strong>不能为空</strong>：一根 K 线只有在某个品种下才唯一
     * @param actor    谁录的（人 / 机器人）。这是「谁干的」，不是「算在谁头上」——
     *                 语音不参与账户统计，所以没有账户参数
     * @throws IllegalArgumentException 没给标的
     */
    public static AudioClip record(String symbolId, String barTime, int seq,
                                   String fileName, int durationMs, long sizeBytes, Actor actor) {
        if (symbolId == null || symbolId.isBlank()) {
            throw new IllegalArgumentException("语音必须挂在某个标的的 K 线上，收到: " + symbolId);
        }
        AudioClip clip = new AudioClip();
        clip.symbolId = symbolId;
        clip.barTime = barTime;
        clip.seq = seq;
        clip.fileName = fileName;
        clip.durationMs = durationMs;
        clip.sizeBytes = sizeBytes;
        clip.actor = actor;
        return clip;
    }
}
