package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.voice.AudioClip;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

/**
 * 语音按<strong>（标的, K 线时间）</strong>取，不按账户 —— 语音挂在 K 线上。
 * 「同一根最多 9 段」数的也是这个组合。
 */
public interface AudioClipRepository extends JpaRepository<AudioClip, String> {

    List<AudioClip> findBySymbolIdAndBarTimeOrderBySeqAsc(String symbolId, String barTime);

    /** 整个标的的语音清单（快照里的 {@code voiceAll}）：按 K 线时间排，横轴下方的录音条照它摆。 */
    List<AudioClip> findBySymbolIdOrderByBarTimeAscSeqAsc(String symbolId);

    /** 录之前先数一下这根 K 线上已经有几条（上限 9，校验在 service）。 */
    long countBySymbolIdAndBarTime(String symbolId, String barTime);

    /** 整个标的录了几条（界面上「共 N 条」）。 */
    long countBySymbolId(String symbolId);
}
