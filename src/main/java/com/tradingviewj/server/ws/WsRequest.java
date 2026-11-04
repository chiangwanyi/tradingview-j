package com.tradingviewj.server.ws;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

import java.math.BigDecimal;

/**
 * 一条上行消息（前端 → 后端）。<strong>所有请求共用一个结构体</strong>，靠 {@code type} 区分。
 *
 * <p>和 Go 版 {@code replay.Request} 同一个套路，理由也一样：每种类型各定义一个类再反射分发，
 * 收益只在「类型安全」，代价是新增一种消息要动三处（类、分发、前端类型）。这里只读该类型
 * 用得到的字段，其余留 null —— 加一条消息，改一个 {@code switch} 分支就够了。
 *
 * <p>{@code @JsonIgnoreProperties(ignoreUnknown = true)}：前端发来的字段比这里多不算错。
 * 少了它，前端给某条消息加一个字段就会让整条消息变成「看不懂」—— 那种失败在浏览器里
 * 只表现为「点了没反应」，最难查。
 *
 * <p><strong>账户 id 每条消息都带</strong>，不从连接上推。一个标签页一个连接，看起来是冗余的，
 * 但它让「连接」和「账户」解耦：同一连接换账户只要再发一条 {@code hello}，
 * 也不必为「断线重连之后这条消息属于谁」引入隐藏状态。
 *
 * <p>下单字段（{@code side} / {@code kind} / {@code price} / {@code quantity} / {@code stopDistance}
 * / {@code orderId}）共用在 {@code order_placed} / {@code market_order} / {@code order_cancelled}
 * / {@code order_moved} 四条消息上。金额用手数用 {@link BigDecimal}：JSON 里 {@code 4000.1}
 * 转 {@code double} 会带上浮点尾巴，而它会直接进 {@code trade.price}。
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public record WsRequest(
        /** 消息类型：hello / step / ping / order_placed / market_order / order_cancelled / order_moved / flat_all / template_* / annotation_* */
        String type,
        /** 哪个 PF 账户的盘面 */
        String accountId,
        /** 连上之后的自我介绍，只在一处日志里用得上 */
        String name,
        /** buy / sell */
        String side,
        /** limit / stop（market 走 {@code market_order} 命令） */
        String kind,
        /** 挂单价（{@code order_placed} / {@code order_moved}） */
        BigDecimal price,
        /** 手数（整数；{@code market_order} / {@code order_placed}） */
        BigDecimal quantity,
        /** 止损距离（点），只在 {@code market_order} 里有意义；&gt; 0 时顺手挂一张保护性止损 */
        BigDecimal stopDistance,
        /** 撤单 / 改价的目标挂单 */
        String orderId,
        /**
         * 图形 id（{@code annotation_deleted} 用）。
         *
         * <p>和 {@link #orderId} 分开而不是共用一个 {@code id}：两条消息的字段名是协议的一部分，
         * 合并会让「删挂单」和「删图形」在前端看起来是同一条命令，而它们的处理路径、
         * 归属（账户 vs 标的）和审计动作完全不同。
         */
        String annotationId,
        /** {@code template_delete} 的目标模板 */
        String templateId,
        /** {@code template_save} 的负载：整条模板（id 为空 = 新建） */
        TemplateCommand template,
        /**
         * {@code annotation_added} / {@code annotation_moved} / {@code annotation_styled}
         * 的负载：一个图形。
         *
         * <p>三条命令共用一个结构，因为它们的负载是同一件事（一个图形的完整状态）——
         * 分成三个 record 只会得到三份一样的字段列表。区别在动作名上，
         * 而动作名由 {@code type} 表达。
         */
        AnnotationCommand annotation,
        /** {@code voice_add}：16kHz/16bit/单声道 WAV 的 base64（一段一分钟约 2.8MB 字符串） */
        String audioData,
        /** {@code voice_add}：前端量到的时长（毫秒）；为空时后端按字节数反推 */
        Integer durationMs
) {
}
