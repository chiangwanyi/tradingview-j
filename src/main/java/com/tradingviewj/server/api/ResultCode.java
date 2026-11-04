package com.tradingviewj.server.api;

import lombok.AllArgsConstructor;
import lombok.Getter;

/**
 * 统一状态码。
 *
 * <p>两段区间，别混着用：
 * <ul>
 *   <li>0 = 成功；400 / 404 / 405 / 415 / 500 = 与 HTTP 语义一一对应的「协议层」错误，
 *       这类错误的 HTTP 状态码也照实返回，方便网关、监控、浏览器直接识别；</li>
 *   <li>10000 起 = 「业务层」错误（余额不足、标的不存在……），HTTP 统一 200，
 *       前端只看 body 里的 code。</li>
 * </ul>
 *
 * <p>为什么业务错误不用 4xx：4xx 在浏览器控制台是红的、在 axios 里直接进 catch，
 * 前端就得把「业务提示」和「网络/协议异常」揉在一个错误分支里处理，很难写。
 */
@Getter
@AllArgsConstructor
public enum ResultCode {

    /** 成功。 */
    SUCCESS(0, "ok"),

    /** 参数不合法（类型不匹配、缺少必填参数、反序列化失败、校验注解不通过）。 */
    PARAM_INVALID(400, "请求参数不合法"),

    /** 路径或资源不存在。 */
    NOT_FOUND(404, "资源不存在"),

    /** HTTP 方法不支持。 */
    METHOD_NOT_ALLOWED(405, "请求方法不支持"),

    /** Content-Type 不支持。 */
    UNSUPPORTED_MEDIA_TYPE(415, "请求体格式不支持"),

    /** 业务处理失败（兜底文案，具体错误请自带 msg）。 */
    BIZ_ERROR(10000, "业务处理失败"),

    /** 服务内部错误（不向外暴露堆栈细节）。 */
    INTERNAL_ERROR(500, "服务内部错误");

    private final int code;
    private final String msg;
}
