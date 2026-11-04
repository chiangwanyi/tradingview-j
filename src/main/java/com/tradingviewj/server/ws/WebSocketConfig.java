package com.tradingviewj.server.ws;

import lombok.RequiredArgsConstructor;
import org.springframework.context.annotation.Configuration;
import org.springframework.web.socket.config.annotation.EnableWebSocket;
import org.springframework.web.socket.config.annotation.WebSocketConfigurer;
import org.springframework.web.socket.config.annotation.WebSocketHandlerRegistry;

/**
 * 把 {@link TradeSocketHandler} 挂到 {@code /ws}。
 *
 * <p>用 {@code spring-boot-starter-websocket} 的这套注册方式（而不是 JSR-356 的
 * {@code @ServerEndpoint}）：handler 由 Spring 管，可以正常注入 service，
 * 端口也直接复用 {@code server.port} 的 8791 —— 不必为一个通道多开一个服务。
 *
 * <h2>为什么允许跨源</h2>
 *
 * <p>开发时前端跑在 Vite（{@code 127.0.0.1:5174}）并通过 proxy 转到 8791，
 * 打包后前端和接口同源。两种情况下 {@code Origin} 头都不一样，写死一个会让另一种连不上。
 * 服务本身<strong>只绑 127.0.0.1</strong>（{@code application.yml} 的 {@code server.address}），
 * 外部访问不到，所以这里按「本机回环的任意端口」放开就够了 —— 不要写成 {@code "*"}：
 * 那等于声明「谁都能连」，而这句话以后换机器部署时没人会回来改。
 *
 * <p>WebSocket <strong>没有</strong>同源策略保护，跨源是服务端说了算的 —— 这条配置就是那道门。
 */
@Configuration
@EnableWebSocket
@RequiredArgsConstructor
public class WebSocketConfig implements WebSocketConfigurer {

    private final TradeSocketHandler handler;

    @Override
    public void registerWebSocketHandlers(WebSocketHandlerRegistry registry) {
        registry.addHandler(handler, "/ws")
                .setAllowedOriginPatterns(
                        "http://127.0.0.1:*",
                        "http://localhost:*");
    }
}
