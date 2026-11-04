package com.tradingviewj.server.exception;

import com.tradingviewj.server.api.ResultCode;
import lombok.Getter;

/**
 * 业务异常：可以预判、可以靠改输入或改状态解决的那类失败。
 *
 * <p>抛出后由 {@link com.tradingviewj.server.web.GlobalExceptionHandler} 接住，
 * 返回 HTTP 200 + {@code {"code":10001,"msg":"余额不足","data":null}}。
 * 这样前端只在 body 里看到一个明确的业务码，不用去猜 HTTP 状态。
 */
@Getter
public class BusinessException extends RuntimeException {

    /** 业务状态码，约定 >= 10000。 */
    private final int code;

    public BusinessException(String msg) {
        this(ResultCode.BIZ_ERROR.getCode(), msg);
    }

    public BusinessException(ResultCode resultCode) {
        this(resultCode.getCode(), resultCode.getMsg());
    }

    public BusinessException(int code, String msg) {
        super(msg);
        this.code = code;
    }

    /** 带上游异常，方便日志里看到根因。 */
    public BusinessException(int code, String msg, Throwable cause) {
        super(msg, cause);
        this.code = code;
    }
}
