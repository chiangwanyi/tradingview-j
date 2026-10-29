package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.voice.AudioClip;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface AudioClipRepository extends JpaRepository<AudioClip, String> {

    List<AudioClip> findByAccountIdAndBarTimeOrderBySeqAsc(String accountId, String barTime);

    /** 录之前先数一下这根 K 线上已经有几条（上限 9，校验在 service）。 */
    long countByAccountIdAndBarTime(String accountId, String barTime);
}
