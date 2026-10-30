package com.tradingviewj.server.controller;

import com.tradingviewj.server.dto.BarSeriesResponse;
import com.tradingviewj.server.dto.SymbolSummaryResponse;
import com.tradingviewj.server.service.BarQueryService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * 行情只读接口：{@code /api/bars}。给 webapp 的交易页画 K 线用。
 *
 * <p><strong>只有两个 GET，没有任何写入口</strong>。K 线是抓取侧（Go 程序）写的 CSV，
 * 这里只按「标的 + 截止时刻」读出来 —— 服务端不缓存、不聚合、不改文件。
 * 「上一交易日是哪个文件」也由这里算（见 {@link BarQueryService}），前端只交一个时刻。
 *
 * <p>为什么不像旧复盘服务那样走 WebSocket：那边要广播（多人同看一块盘面、谁改了哪根），
 * 而这里是单用户看自己账户的图，一问一答的 HTTP 已经够了，
 * 也就不用为一个画布再引一套连接状态机。
 */
@Tag(name = "行情", description = "K 线只读接口：标的清单 + 从上一交易日到指定时刻的 5 分钟 K 线")
@RestController
@RequestMapping("/api/bars")
@RequiredArgsConstructor
public class BarController {

    private final BarQueryService bars;

    @Operation(summary = "标的清单", description = """
            来自 data/_symbols.json，附带每个标的磁盘上有多少个日文件、首尾是哪两天。
            清单是抓取侧维护的，这里不落库；文件不存在时返回空数组。
            """)
    @GetMapping("/symbols")
    public List<SymbolSummaryResponse> symbols() {
        return bars.symbols();
    }

    @Operation(summary = "上一交易日到某个时刻的 K 线", description = """
            code 必填（_symbols.json 的键，如 CME_MINI:MNQ1!）；time 是截止时刻，格式 yyyy-MM-dd HH:mm
            （如 2025-12-04 09:00），不传就是磁盘上最新的一天整天。
            返回的是「上一交易日的整天 + 截止那天从开盘到 time 的那一段」，上一交易日按磁盘上的 CSV 文件算，
            不是日历减一天。休市 / 没有文件的那一天返回空数组，不是错误 —— 日历上本来就有些天没有数据。
            """)
    @GetMapping
    public BarSeriesResponse day(
            @RequestParam String code,
            @RequestParam(required = false) String time) {
        return bars.day(code, time);
    }
}
