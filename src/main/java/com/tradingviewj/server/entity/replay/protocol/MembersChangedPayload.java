package com.tradingviewj.server.entity.replay.protocol;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.Data;
import lombok.NoArgsConstructor;

/**
 * 在线成员变了（对应 Go 的 {@code replay.MembersChangedPayload}）。
 */
@Data
@Builder
@NoArgsConstructor
@AllArgsConstructor
@JsonInclude(JsonInclude.Include.ALWAYS)
public final class MembersChangedPayload {

    private List<Member> members;
}
