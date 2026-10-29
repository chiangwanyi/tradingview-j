package com.tradingviewj.server.controller;

import cn.hutool.core.bean.BeanUtil;
import cn.hutool.core.util.StrUtil;
import com.tradingviewj.server.api.R;
import com.tradingviewj.server.entity.VersionResponse;
import com.tradingviewj.server.exception.BusinessException;
import lombok.Data;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.Map;

/**
 * 只在测试里存在的接口，用来验证「成功/业务异常/未预料异常/参数错误」四种情况结构一致。
 *
 * <p>放在 src/test 下，不打进生产 jar；也不用往真实 API 里塞 demo 路径。
 */
@RestController
@RequestMapping("/test")
class DemoApiController {

    @GetMapping("/string")
    String string() {
        return "hello";
    }

    @GetMapping("/object")
    VersionResponse object() {
        return VersionResponse.builder().version("9.9.9").application("demo").build();
    }

    @GetMapping("/wrapped")
    R<Map<String, Object>> wrapped() {
        return R.ok(Map.of("k", "v"));
    }

    @GetMapping("/biz-error")
    VersionResponse bizError() {
        throw new BusinessException(10001, "余额不足");
    }

    @GetMapping("/boom")
    VersionResponse boom() {
        throw new IllegalStateException("db down: jdbc:mysql://10.0.0.1:3306");
    }

    @GetMapping("/num/{n}")
    int num(@PathVariable int n) {
        return n;
    }

    @GetMapping("/bean")
    VersionView bean() {
        VersionResponse source = VersionResponse.builder().version("9.9.9").application("demo").build();
        // Hutool 的对象转换：同名字段直接拷，不用手写一长串 getter/setter
        VersionView view = BeanUtil.copyProperties(source, VersionView.class);
        view.setLabel(StrUtil.format("{}-{}", view.getApplication(), source.getVersion()));
        return view;
    }

    @Data
    public static class VersionView {
        private String application;
        private String label;
    }
}
