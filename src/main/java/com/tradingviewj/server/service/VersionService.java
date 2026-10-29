package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.entity.VersionResponse;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/**
 * 系统信息服务。
 *
 * <p>Controller 只负责「收请求、给响应」，拼业务数据放这里。目前只有版本号一件事，
 * 也刻意经过一层：等以后有了账户、标的、交易这些计算，才不会出现「逻辑散在 Controller 里」。
 */
@Service
@RequiredArgsConstructor
public class VersionService {

    private final AppProperties appProperties;

    /** 当前运行的版本信息，版本号来自 pom.xml，见 {@link AppProperties}。 */
    public VersionResponse currentVersion() {
        return VersionResponse.builder()
                .version(appProperties.getVersion())
                .application(appProperties.getName())
                .build();
    }
}
