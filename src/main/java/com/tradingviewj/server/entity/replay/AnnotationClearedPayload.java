package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 「清空全部绘图」的广播负载（对应 Go 的 {@code replay.AnnotationClearedPayload}）。
 *
 * <p>不是连发 N 条 annotation_deleted：那样会在操作记录里留下 N 行噪音，
 * 中途断线还会留下「删了一半」的状态。一次清空 = 一条广播 = 一个原子状态。
 * 带上被删掉的编号，事后回看才知道「清的是哪 5 个」。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class AnnotationClearedPayload {

    private int count;

    private List<String> annotationIds;
}
