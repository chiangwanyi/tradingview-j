package com.tradingviewj.server.dto;

import java.math.BigDecimal;
import java.util.List;

/**
 * 一个标的、一个交易日的全部 K 线。
 *
 * <p>为什么是「一天一份」而不是一段区间：CSV 本来就是一天一个文件，
 * 交易日的边界（以及休市那 12 根的空档）已经在文件里定死了。让调用方拼区间，
 * 就得在服务端做一次跨文件拼接 + 缺口判断 —— 那是另一件事，画布并不需要。
 *
 * <p>休市 / 没有数据的那一天返回 {@code bars: []}，<strong>不是错误</strong>：
 * 日历上确实有些天没有文件，前端画一张空图并写明「这一天没有数据」即可。
 *
 * @param code      标的代码
 * @param folder    {@code data/} 下的目录名
 * @param day       实际读的那一天（{@code yyyy-MM-dd}）；请求没指定时是磁盘上最新的一天
 * @param tick      最小跳动；没有配置时为 null
 * @param tickValue 一跳值多少美元；没有配置时为 null
 * @param bars      按时间升序
 */
public record BarSeriesResponse(
        String code,
        String folder,
        String day,
        BigDecimal tick,
        BigDecimal tickValue,
        List<BarView> bars
) {
}
