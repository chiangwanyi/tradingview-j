package com.tradingviewj.server.config;

import io.swagger.v3.oas.models.OpenAPI;
import io.swagger.v3.oas.models.info.Info;
import io.swagger.v3.oas.models.media.IntegerSchema;
import io.swagger.v3.oas.models.media.ObjectSchema;
import io.swagger.v3.oas.models.media.Schema;
import io.swagger.v3.oas.models.media.StringSchema;
import io.swagger.v3.oas.models.servers.Server;
import org.springdoc.core.customizers.OperationCustomizer;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import java.util.List;

/**
 * Swagger / OpenAPI 文档的整体定义。
 *
 * <p>这里只做两件事：
 * <ol>
 *   <li>给文档起名字、填版本、标明服务地址（{@link #tradingviewOpenApi}）；</li>
 *   <li>修掉「文档说的响应」和「实际返回的响应」不一致的问题
 *       （{@link #envelopeAwareResponseCustomizer}）——见下面详细说明。</li>
 * </ol>
 *
 * <p>接口本身的说明（分组、摘要、字段含义）不写在这里，而是用 {@code @Tag} / {@code @Operation} /
 * {@code @Schema} 标在 Controller 和 DTO 上。理由和统一响应体一样：说明跟着代码走，
 * 改接口时不可能忘记改文档，也不用在另一个文件里找对应关系。
 */
@Configuration
public class OpenApiConfig {

    /** 只有本项目自己的 Controller 才需要「补上外层包裹」，框架和 springdoc 自己的端点不碰。 */
    private static final String BASE_PACKAGE = "com.tradingviewj.server";

    /** 统一响应体在 components 里的名字，取自 {@code R} 的类名。 */
    private static final String ENVELOPE_REF = "#/components/schemas/R";

    @Bean
    public OpenAPI tradingviewOpenApi(AppProperties appProperties,
                                     @Value("${server.address:127.0.0.1}") String address,
                                     @Value("${server.port:8791}") int port) {
        return new OpenAPI()
                .info(new Info()
                        .title("tradingview-j-server")
                        .version(appProperties.getVersion())
                        .description("""
                                Go 版 tradingview-j 的 Java / Spring Boot 迁移目标。

                                所有接口的响应都经过统一包裹，形如：
                                `{"code":0,"msg":"ok","data":{...}}`

                                - `code`：0 表示成功；400/404/405/415/500 与 HTTP 语义一致；
                                  10000 起是业务错误（此时 HTTP 状态码仍是 200）
                                - `msg`：给用户看的提示，永不为 null
                                - `data`：业务数据，失败时为 null，但字段本身仍然存在
                                """))
                .addServersItem(new Server()
                        .url("http://" + address + ":" + port)
                        .description("本机（服务只绑回环地址，外部访问不到）"));
    }

    /**
     * 把统一响应体「还原」进文档。
     *
     * <p>为什么需要它：Controller 方法返回的是裸业务对象（比如 {@code VersionResponse}），
     * 外层 {@code code/msg/data} 是 {@code GlobalResponseAdvice} 在运行期补上的。
     * springdoc 只看得见方法签名，于是文档里写的是「响应 = VersionResponse」，
     * 和真实返回「响应 = R&lt;VersionResponse&gt;」差了一层。
     * 这种不一致比没有文档更糟：前端照着文档写 {@code res.version}，实际拿到的是 {@code res.data.version}。
     *
     * <p>做法：springdoc 生成完 operation 之后，把里面每个响应体的 schema 拿出来，
     * 塞进一个只有 code/msg/data 三个字段的外壳，原来的 schema 挂到 {@code data} 上。
     * 原来是 {@code $ref: VersionResponse} 的，这里仍然是同一个 {@code $ref}，模型不会被复制一份。
     */
    @Bean
    public OperationCustomizer envelopeAwareResponseCustomizer() {
        return (operation, handlerMethod) -> {
            if (!handlerMethod.getBeanType().getPackageName().startsWith(BASE_PACKAGE)
                    || operation.getResponses() == null) {
                return operation;
            }
            operation.getResponses().values().forEach(response -> {
                if (response.getContent() == null) {
                    return;
                }
                // 接口可能有多种 Content-Type（json / xml），逐个处理，别只改第一个
                response.getContent().values().forEach(mediaType ->
                        mediaType.setSchema(wrapWithEnvelope(mediaType.getSchema())));
            });
            return operation;
        };
    }

    /**
     * 给一个「业务数据的 schema」套上 code/msg/data 外壳。
     *
     * @param dataSchema 原 schema，可能是 {@code $ref}，也可能是 null（接口没有返回体）
     */
    private static Schema<?> wrapWithEnvelope(Schema<?> dataSchema) {
        // 幂等：Controller 自己已经声明返回 R 的，不再套第二层
        if (dataSchema != null && ENVELOPE_REF.equals(dataSchema.get$ref())) {
            return dataSchema;
        }
        ObjectSchema envelope = new ObjectSchema();
        envelope.addProperty("code", new IntegerSchema()
                .description("业务状态码，0 = 成功")
                .example(0));
        envelope.addProperty("msg", new StringSchema()
                .description("提示文案，永不为 null")
                .example("ok"));
        envelope.addProperty("data", dataSchema == null
                ? new ObjectSchema().description("业务数据，失败时为 null")
                : dataSchema);
        return envelope.required(List.of("code", "msg", "data"));
    }
}
