package com.tradingviewj.server.dto;

import java.math.BigDecimal;
import java.util.List;

/**
 * 一个标的从<strong>上一交易日整天开始</strong>、到<strong>请求给的时刻</strong>为止的 K 线。
 *
 * <p>为什么是一段而不是一天：CSV 一天一个文件，但交易页要看的是「接着上次的位置继续」——
 * 只给当天，图上就没有隔夜的收/开盘和昨晚的走势做参照。所以服务端把上一交易日（按磁盘上的
 * CSV 算，不是日历减一）整天接在前面，再用请求里的时刻把当天截断。
 *
 * <p>响应结构<strong>没有变</strong>：还是 {@code code / folder / day / tick / tickValue / bars}，
 * 只是 {@code bars} 现在可能跨两个日文件。
 *
 * <p>休市 / 没有数据的那一天返回 {@code bars: []}，<strong>不是错误</strong>：
 * 日历上确实有些天没有文件，前端画一张空图并写明「这一天没有数据」即可。
 *
 * @param code      标的代码
 * @param folder    {@code data/} 下的目录名
 * @param day       截止那天（{@code yyyy-MM-dd}）；请求没传时刻时是磁盘上最新的一天
 * @param tick      最小跳动；没有配置时为 null
 * @param tickValue 一跳值多少美元；没有配置时为 null
 * @param bars      按时间升序；第一根可能来自上一个交易日
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
