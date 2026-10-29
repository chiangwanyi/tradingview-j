package com.tradingviewj.server.entity.store.catalog;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * _symbols.json 里的一条标的登记（对应 Go 的 {@code store.symRec}，磁盘格式，不能改名）。
 *
 * <p>tick / tickValue 由用户手填，抓取这边**原样保留**：写索引时要带回去，
 * 否则抓一次就把用户填的值抹掉了。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class SymRec {

    private String folder;

    /** 最小跳动（0.25 / 0.1 / 0.01…），复盘的价格轴要用它决定小数位 */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double tick;

    /** 一个 tick 值多少美元（MNQ 0.50、MES 1.25、MGC 1.00、CL 10.00） */
    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private double tickValue;

    @JsonInclude(JsonInclude.Include.NON_DEFAULT)
    private long created;
}
