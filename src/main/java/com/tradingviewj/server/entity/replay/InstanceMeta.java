package com.tradingviewj.server.entity.replay;

import java.util.List;
import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * meta.json：一个实例「除了流水之外」的全部状态（对应 Go 的 {@code replay.instanceMeta}）。
 *
 * <p>字段名写全，方便直接打开文件看。属于进程的东西（操作者、机器人协程）不在这里。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class InstanceMeta {

    private String id;

    private String symbol;

    private String folder;

    private double tick;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double tickValue;

    private String anchor;

    private int cursor;

    private Position position;

    private double realizedPnl;

    private String createdBy;

    private long createdAt;

    private int seq;

    /** 加载过程中的实情（坏行、空文件），原样交给界面 */
    private List<String> notes;

    /** 标注编号的下一个值，三个 seq 分别对应标注 / 挂单 / 成交 */
    private int seqAnnotation;

    private int seqOrder;

    private int seqTrade;
}
