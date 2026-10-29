package com.tradingviewj.server.entity.replay;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;
import java.util.Map;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 一条上行消息（对应 Go 的 {@code replay.Request}）：每种 type 只读自己那几个字段。
 *
 * <p>用一个宽结构而不是每种消息一个类，是为了让协议只有一处定义；
 * 加字段不需要新增类型，前端多传一个不认识的键也不会报错。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class Request {

    private String type;

    // ---- hello ----
    private String name;

    // ---- create_instance ----
    private String symbol;

    /** 空 = 由后端随机挑一个时间点 */
    private String time;

    /**
     * true = 锚点取「数据目录里最新的那一根 K 线」（界面上的【新增实例（最新）】），
     * 这时 time 和 seed 都不起作用。
     *
     * <p>不在前端算时间：最新一根的时刻只存在于 CSV 内容里，文件名只到「哪一天」。
     * 让前端拼一个「最后一天 + 某个钟点」出来，遇到那天提前收市就会指向一根不存在的 K 线。
     */
    private boolean latest;

    /**
     * true = 锚点取「随机一个交易日的开盘首根」（【新增实例（开盘）】）。
     * 不看 time：开盘那根在几点只有读 CSV 才知道；但 seed 认——它挑哪一天是随机的，得能复现。
     */
    private boolean session;

    /** 画布上初始显示多少根（含锚点之前的历史），0 = 默认 */
    private int bars;

    /** 锚点之后预加载几天，0 = 默认 */
    private int forwardDays;

    /** 非 0 时随机锚点可复现（测试/复现问题用） */
    private long seed;

    // ---- 所有和某个实例有关的请求 ----
    private String instanceId;

    // ---- annotation_add / annotation_move ----
    /** trend | ray | rect | arrow | fib */
    private String kind;

    private Point start;

    private Point end;

    /** annotation_add 是画下去那一刻的快照；annotation_style 是换上的新样式 */
    private AnnotationStyle style;

    /** true = 端点吸附到该 K 线的 OHLC（前端按住 Ctrl） */
    private boolean snap;

    // ---- annotation_move / annotation_delete / annotation_style ----
    private String annotationId;

    // ---- save_template ----
    private Template template;

    // ---- delete_template ----
    private String templateId;

    // ---- order_place / order_market / bot_start ----
    /** buy | sell */
    private String side;

    private double price;

    /** 挂单手数；bot_start 时是「策略每笔下多少手」 */
    private double quantity;

    /**
     * order_market 的保护性止损距离（点）。&gt; 0 时后端在同一次动作里先市价成交、
     * 再按「成交价 ∓ 距离」挂一张反方向的 stop 单。
     *
     * <p>传距离而不是传价格：ATR 只在界面上算，后端不知道 n 是多少；
     * 而成交价必须用后端自己那个（四舍五入到 tick），否则止损位会差一跳。
     */
    private double stopDistance;

    // ---- order_cancel / order_modify ----
    private String orderId;

    // ---- bot_start ----
    /** 0 = 默认节拍 */
    private int intervalMs;

    /** 空 = 随机演示机器人；非空是策略 id */
    private String strategy;

    /** 策略参数覆盖；缺省用策略自己的默认值 */
    private Map<String, Double> params;

    // ---- record_audio ----
    /** 一段 WAV 的 base64。不走文件上传：录音只有几十 KB 到几百 KB，走这条有序消息流顺序天然对得上 */
    private String audioData;

    private int durationMs;

    // ---- get_recording / delete_recording ----
    private String recordingId;

    /** recording_extend 一次往后加载几根（0 = 1 根，上限由后端定） */
    private int count;

    /** set_recording_tags：这笔交易之后应该有的那一组标签（空数组 = 全部清掉） */
    private List<RecordTag> tags;
}
