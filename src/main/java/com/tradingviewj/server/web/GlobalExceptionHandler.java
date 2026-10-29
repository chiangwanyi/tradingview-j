package com.tradingviewj.server.web;

import cn.hutool.core.collection.CollUtil;
import cn.hutool.core.util.StrUtil;
import com.tradingviewj.server.api.R;
import com.tradingviewj.server.api.ResultCode;
import com.tradingviewj.server.exception.BusinessException;
import jakarta.servlet.http.HttpServletRequest;
import lombok.extern.slf4j.Slf4j;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.validation.BindException;
import org.springframework.validation.BindingResult;
import org.springframework.validation.FieldError;
import org.springframework.web.HttpMediaTypeNotSupportedException;
import org.springframework.web.HttpRequestMethodNotSupportedException;
import org.springframework.web.bind.MissingServletRequestParameterException;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.method.annotation.MethodArgumentTypeMismatchException;
import org.springframework.web.servlet.NoHandlerFoundException;
import org.springframework.web.servlet.resource.NoResourceFoundException;

import java.util.stream.Collectors;

/**
 * 全局异常处理：把「异常」也变成 {@code code/msg/data} 结构。
 *
 * <p>这是保证「成功与异常返回结构一致」的另一半。只有 {@link GlobalResponseAdvice} 的话，
 * 一旦抛异常，Spring 会走自己的错误页 / {@code /error} 端点，前端拿到的是完全另一种 JSON。
 *
 * <p>HTTP 状态码分两类：
 * <ul>
 *   <li>业务异常 → HTTP 200，错误信息全在 body 的 code 里；</li>
 *   <li>协议层/系统异常 → 状态码照实给（400/404/405/415/500），body 结构不变。</li>
 * </ul>
 */
@Slf4j
@RestControllerAdvice
public class GlobalExceptionHandler {

    /** 业务异常：能预判、能靠改输入或改状态解决的失败。HTTP 恒为 200。 */
    @ExceptionHandler(BusinessException.class)
    public ResponseEntity<R<Void>> handleBusiness(BusinessException ex, HttpServletRequest request) {
        log.warn("business error, uri={}, code={}, msg={}", request.getRequestURI(), ex.getCode(), ex.getMessage());
        return ResponseEntity.ok(R.fail(ex.getCode(), ex.getMessage()));
    }

    /**
     * 参数绑定/校验失败。
     *
     * <p>{@code @Valid} 抛出的 {@code MethodArgumentNotValidException} 是
     * {@link BindException} 的子类，这一个方法就都覆盖了。
     */
    @ExceptionHandler(BindException.class)
    public ResponseEntity<R<Void>> handleBind(BindException ex, HttpServletRequest request) {
        String detail = describeFieldErrors(ex.getBindingResult());
        log.warn("bind error, uri={}, detail={}", request.getRequestURI(), detail);
        return badRequest(detail);
    }

    /** 参数类型不对（路径上写了字母但方法要 int）、请求体读不出来、漏了必填 query 参数。 */
    @ExceptionHandler({
            MethodArgumentTypeMismatchException.class,
            HttpMessageNotReadableException.class,
            MissingServletRequestParameterException.class
    })
    public ResponseEntity<R<Void>> handleBadRequest(Exception ex, HttpServletRequest request) {
        String detail;
        if (ex instanceof MethodArgumentTypeMismatchException mismatch) {
            Class<?> required = mismatch.getRequiredType();
            detail = StrUtil.format("参数 {} 类型不正确，期望 {}",
                    mismatch.getName(),
                    required == null ? "未知类型" : required.getSimpleName());
        } else if (ex instanceof MissingServletRequestParameterException missing) {
            detail = StrUtil.format("缺少必填参数 {}", missing.getParameterName());
        } else {
            detail = "请求体无法解析";
        }
        log.warn("bad request, uri={}, msg={}", request.getRequestURI(), ex.getMessage());
        return badRequest(detail);
    }

    /** 路径不存在。Spring 6.1 起静态资源未命中抛的是 {@code NoResourceFoundException}。 */
    @ExceptionHandler({NoHandlerFoundException.class, NoResourceFoundException.class})
    public ResponseEntity<R<Void>> handleNotFound(Exception ex, HttpServletRequest request) {
        log.warn("not found, uri={}", request.getRequestURI());
        return ResponseEntity.status(HttpStatus.NOT_FOUND).body(R.fail(ResultCode.NOT_FOUND));
    }

    /** 方法不支持，比如用 GET 打 POST 接口。 */
    @ExceptionHandler(HttpRequestMethodNotSupportedException.class)
    public ResponseEntity<R<Void>> handleMethodNotAllowed(HttpRequestMethodNotSupportedException ex,
                                                         HttpServletRequest request) {
        log.warn("method not allowed, uri={}, method={}", request.getRequestURI(), ex.getMethod());
        return ResponseEntity.status(HttpStatus.METHOD_NOT_ALLOWED).body(R.fail(ResultCode.METHOD_NOT_ALLOWED));
    }

    /** Content-Type 不支持，比如没写 application/json 就发 JSON。 */
    @ExceptionHandler(HttpMediaTypeNotSupportedException.class)
    public ResponseEntity<R<Void>> handleMediaType(HttpMediaTypeNotSupportedException ex,
                                                  HttpServletRequest request) {
        log.warn("unsupported media type, uri={}, contentType={}", request.getRequestURI(), ex.getContentType());
        return ResponseEntity.status(HttpStatus.UNSUPPORTED_MEDIA_TYPE)
                .body(R.fail(ResultCode.UNSUPPORTED_MEDIA_TYPE));
    }

    /**
     * 兜底：任何没被上面接住的异常。
     *
     * <p>对外只给固定文案，堆栈只进日志——异常 message 里经常带 SQL、路径、内网地址，
     * 原样返回等于免费给攻击者做侦察。
     */
    @ExceptionHandler(Exception.class)
    public ResponseEntity<R<Void>> handleUnexpected(Exception ex, HttpServletRequest request) {
        log.error("unexpected error, uri={}", request.getRequestURI(), ex);
        return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(R.fail(ResultCode.INTERNAL_ERROR));
    }

    private static ResponseEntity<R<Void>> badRequest(String detail) {
        return ResponseEntity.badRequest()
                .body(R.fail(ResultCode.PARAM_INVALID.getCode(),
                        StrUtil.blankToDefault(detail, ResultCode.PARAM_INVALID.getMsg())));
    }

    /** 把字段错误拼成一句人话："version 不能为空; price 必须大于 0"。 */
    private static String describeFieldErrors(BindingResult bindingResult) {
        if (bindingResult == null || CollUtil.isEmpty(bindingResult.getFieldErrors())) {
            return ResultCode.PARAM_INVALID.getMsg();
        }
        return bindingResult.getFieldErrors().stream()
                .map(GlobalExceptionHandler::describeFieldError)
                .collect(Collectors.joining("; "));
    }

    private static String describeFieldError(FieldError fieldError) {
        return StrUtil.format("{} {}", fieldError.getField(),
                StrUtil.blankToDefault(fieldError.getDefaultMessage(), "不合法"));
    }
}
