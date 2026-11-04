package com.tradingviewj.server.api;

import cn.hutool.core.util.StrUtil;
import com.fasterxml.jackson.annotation.JsonInclude;
import lombok.Data;

import java.io.Serializable;

/**
 * 统一响应包裹：{@code {"code":0,"msg":"ok","data":...}}。
 *
 * <p>三个字段永远都在。写代码时不用手 new，走 {@link #ok()} / {@link #fail} 系列静态方法。
 *
 * <p>注意一个坑：application.yml 里配了
 * {@code spring.jackson.default-property-inclusion: non_null}，全局会把值为 null 的字段整个丢掉。
 * 对普通业务对象这没问题，但对这个包裹体不行——失败时 {@code data} 会消失，
 * 前端就得写 {@code 'data' in r} 这种判断。所以 {@link #data} 上单独标注 ALWAYS，
 * 字段级注解优先级高于全局配置。
 *
 * @param <T> 业务数据类型
 */
@Data
public class R<T> implements Serializable {

    private static final long serialVersionUID = 1L;

    /** 业务状态码，0 = 成功，见 {@link ResultCode}。 */
    private int code;

    /** 给用户看的提示，永远不为 null。 */
    private String msg;

    /** 业务数据，失败时为 null，但字段本身仍在 JSON 里。 */
    @JsonInclude(JsonInclude.Include.ALWAYS)
    private T data;

    /** Jackson 反序列化需要无参构造（比如测试里把响应体读回来）。 */
    public R() {
    }

    private R(int code, String msg, T data) {
        this.code = code;
        this.msg = msg;
        this.data = data;
    }

    /** 成功且没有数据（新增/删除这类接口用）。 */
    public static <T> R<T> ok() {
        return new R<>(ResultCode.SUCCESS.getCode(), ResultCode.SUCCESS.getMsg(), null);
    }

    /** 成功并携带数据。 */
    public static <T> R<T> ok(T data) {
        return new R<>(ResultCode.SUCCESS.getCode(), ResultCode.SUCCESS.getMsg(), data);
    }

    /** 失败，文案取状态码枚举里的默认文案。 */
    public static <T> R<T> fail(ResultCode resultCode) {
        return new R<>(resultCode.getCode(), resultCode.getMsg(), null);
    }

    /**
     * 失败，自定义状态码与文案。
     *
     * <p>msg 传空时兜底成 "error"，避免前端弹出一个空白 toast（Hutool 的 blankToDefault 一并处理了 null）。
     */
    public static <T> R<T> fail(int code, String msg) {
        return new R<>(code, StrUtil.blankToDefault(msg, "error"), null);
    }
}
