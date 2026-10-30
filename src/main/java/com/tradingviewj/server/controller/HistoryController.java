package com.tradingviewj.server.controller;

import com.tradingviewj.server.api.R;
import com.tradingviewj.server.dto.AuditEventView;
import com.tradingviewj.server.dto.PageResponse;
import com.tradingviewj.server.dto.TradeRecordView;
import com.tradingviewj.server.service.HistoryQueryService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

/**
 * 历史记录接口：{@code /api/history}。左菜单的【操作记录】【交易记录】两个页面用它。
 *
 * <p>两张表都是<strong>跨账户</strong>的全量列表（不按 accountId 过滤）——
 * 这两个页面的用途就是「回头翻一遍我都干了什么」。
 *
 * <p>唯一的写操作是清空操作记录（用户手动触发），其余都是 GET。
 */
@Tag(name = "历史记录", description = "操作记录 / 交易记录的全量分页查看，以及清空操作记录")
@RestController
@RequestMapping("/api/history")
@RequiredArgsConstructor
public class HistoryController {

    private final HistoryQueryService history;

    @Operation(summary = "操作记录（分页）", description = """
            page 从 0 起，size 默认 50、最多 200；按 created_at 倒序（新的在前）。
            action / actor 是小写协议串（order_placed / human），和 WS 广播同一套写法。
            accountName 是现查的账户名，账户已删时为 null（此时前端显示 accountId 尾部）。
            """)
    @GetMapping("/audit-events")
    public PageResponse<AuditEventView> auditEvents(
            @RequestParam(defaultValue = "0") int page,
            @RequestParam(defaultValue = "50") int size) {
        return history.auditEvents(page, size);
    }

    @Operation(summary = "清空操作记录", description = """
            **不可逆**：整张 audit_event 清空，返回删掉几行。
            不影响交易记录（它的区间锚在 trade.seq 上，不是 audit_event.seq），
            也不影响盘面（挂单 / 成交 / 图形各有自己的表）。
            清空后各账户的 seq 从 0 重新开始。
            """)
    @DeleteMapping("/audit-events")
    public R<Long> clearAuditEvents() {
        return R.ok(history.clearAuditEvents());
    }

    @Operation(summary = "交易记录（分页）", description = """
            page 从 0 起，size 默认 50、最多 200；按 entry_bar_time 倒序。
            只有摘要（方向 / 手数 / 进出价 / 毛利 / 持仓根数 / 是否已平），不含每一步明细。
            """)
    @GetMapping("/trade-records")
    public PageResponse<TradeRecordView> tradeRecords(
            @RequestParam(defaultValue = "0") int page,
            @RequestParam(defaultValue = "50") int size) {
        return history.tradeRecords(page, size);
    }
}
