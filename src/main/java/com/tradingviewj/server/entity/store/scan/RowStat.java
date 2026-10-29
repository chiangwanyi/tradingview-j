package com.tradingviewj.server.entity.store.scan;

import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 读一个日文件的副产品（对应 Go 的 {@code store.RowStat}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public class RowStat {

    /** 读到的数据行数（不含表头） */
    private int rows;

    /** 解析不了、被跳过的行数 */
    private int bad;
}
