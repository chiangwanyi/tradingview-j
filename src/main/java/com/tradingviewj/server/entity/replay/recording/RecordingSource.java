package com.tradingviewj.server.entity.replay.recording;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * 一笔交易记录的操作来源（对应 Go 的 {@code SourceHuman} / {@code SourceBot}）。
 *
 * <p>为什么不从 {@code createdBy} 猜：createdBy 是「谁开的这一笔」，来源问的是「这些操作是谁做的」。
 * 真人开的实例被机器人接管后，那一段也该算机器人——所以来源在开仓那一刻就定下来存进记录。
 *
 * <p>只有两个取值：机器人再多，前端筛选也只分「真人 / 机器人」两栏，细分在 sourceName。
 */
public enum RecordingSource {

    @JsonProperty("human")
    HUMAN,

    @JsonProperty("bot")
    BOT
}
