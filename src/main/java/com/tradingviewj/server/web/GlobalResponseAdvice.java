package com.tradingviewj.server.web;

import cn.hutool.json.JSONUtil;
import com.tradingviewj.server.api.R;
import lombok.extern.slf4j.Slf4j;
import org.springframework.core.MethodParameter;
import org.springframework.core.io.Resource;
import org.springframework.http.MediaType;
import org.springframework.http.converter.HttpMessageConverter;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.servlet.ModelAndView;
import org.springframework.web.servlet.mvc.method.annotation.ResponseBodyAdvice;
import org.springframework.web.servlet.mvc.method.annotation.StreamingResponseBody;

/**
 * 全局响应包裹：Controller 只管返回业务对象，包裹在这里统一加。
 *
 * <p>为什么用 {@link ResponseBodyAdvice} 而不是让每个 Controller 写 {@code return R.ok(...)}：
 * 包裹是「协议约定」，不是业务逻辑。放在这一层做，Controller 签名保持干净，
 * 新增接口不可能忘记包，也不会出现「一半包了一半没包」。
 *
 * <p>它拦的是「已经确定要写进响应体」的返回值，所以异常处理器返回的 {@code R} 也会经过这里，
 * 靠 {@link #beforeBodyWrite} 里的幂等判断保证不会包两层。
 */
@Slf4j
@RestControllerAdvice
public class GlobalResponseAdvice implements ResponseBodyAdvice<Object> {

    /**
     * 这些包下的 Controller 不参与包裹。
     *
     * <p>{@code org.springframework.boot}：{@code /error} 端点、actuator，它们有自己的结构，包了会变形。
     *
     * <p>{@code org.springdoc}：这是 Swagger 文档自己的端点。{@code /v3/api-docs} 返回的就是一份完整的
     * OpenAPI JSON，一旦被包成 {@code {"code":0,"msg":"ok","data":{...}}}，Swagger UI 在前端找不到
     * 顶层的 {@code openapi} 字段，页面会直接白屏——而且浏览器控制台只报一句「找不到 spec」，
     * 很难联想到是统一响应体干的。加这一条是接入文档时最容易踩、也最容易漏的一步。
     */
    private static final String[] UNWRAPPED_PACKAGES = {
            "org.springframework.boot",
            "org.springdoc"
    };

    /**
     * 决定哪些返回值要走包裹。
     *
     * <p>三类必须排除：
     * <ul>
     *   <li>框架自己的端点（见 {@link #UNWRAPPED_PACKAGES}）——它们有自己的结构，包了会变形；</li>
     *   <li>二进制/流式返回：文件下载、{@code byte[]}、{@code StreamingResponseBody}——包成 JSON 就废了；</li>
     *   <li>{@code ModelAndView}——那是页面渲染，不是 JSON 接口。</li>
     * </ul>
     */
    @Override
    public boolean supports(MethodParameter returnType, Class<? extends HttpMessageConverter<?>> converterType) {
        String declaringClassName = returnType.getContainingClass().getName();
        for (String packagePrefix : UNWRAPPED_PACKAGES) {
            if (declaringClassName.startsWith(packagePrefix)) {
                return false;
            }
        }
        Class<?> bodyType = returnType.getParameterType();
        return !(Resource.class.isAssignableFrom(bodyType)
                || byte[].class == bodyType
                || ModelAndView.class.isAssignableFrom(bodyType)
                || StreamingResponseBody.class.isAssignableFrom(bodyType));
    }

    @Override
    public Object beforeBodyWrite(Object body,
                                  MethodParameter returnType,
                                  MediaType selectedContentType,
                                  Class<? extends HttpMessageConverter<?>> selectedConverterType,
                                  org.springframework.http.server.ServerHttpRequest request,
                                  org.springframework.http.server.ServerHttpResponse response) {

        // 1. 已经是 R 了就直接放行 —— Controller 手工包过、或异常处理器返回的，都走这条。
        //    没有这个判断，异常响应会变成 {"code":0,"msg":"ok","data":{"code":500,...}}。
        if (body instanceof R<?>) {
            return body;
        }

        // 2. String 返回值：选中的是 StringHttpMessageConverter，往里塞 R 会 ClassCastException。
        //    这里先自己序列化成 JSON 文本再交出去，前端拿到的仍是同一个包裹结构。
        //    （Hutool 的 JSONUtil 直接复用 Jackson 之外的序列化，不需要额外写 ObjectMapper 注入。）
        if (body instanceof String text) {
            return JSONUtil.toJsonStr(R.ok(text));
        }

        // 3. void 方法（只写响应、不返回数据），给一个空 data 的成功包裹，别让 body 是空白。
        if (body == null && void.class.equals(returnType.getParameterType())) {
            return R.ok();
        }

        // 4. 其余一律包裹；body 为 null 时得到 data:null，字段依然存在。
        return R.ok(body);
    }
}
