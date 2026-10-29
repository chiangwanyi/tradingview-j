# tradingview-j-server

Go 版 `tradingview-j` 的 Java / Spring Boot 迁移目标。当前只有一条链路：起得来、打日志、读配置、返回版本号。

工程文件（`pom.xml`、`src/`、`mvnw`）就在**仓库根目录**，和 Go 模块共用同一个根：

- Java 侧走 Maven：`src/`、`target/`、`mvnw`
- Go 侧走 go build：`main.go`、`internal/`、`build.cmd`/`build.sh`
- 两边互不引用，各自的产物都在自己的忽略规则里

## 跑

```bash
./mvnw spring-boot:run      # Windows: mvnw.cmd spring-boot:run
```

访问 http://127.0.0.1:8791/api/version → `{"version":"1.0.0","application":"tradingview-j-server"}`

打包：`./mvnw package` → `target/tv-server.jar`，`java -jar target/tv-server.jar`。

## 版本号只有一处

`pom.xml` 的 `<version>` → Maven 资源过滤写入 `application.yml` 的 `app.version` → `AppProperties` → Controller。
升版本只改 pom。测试 `VersionControllerTest` 钉住 1.0.0，过滤失效会当场红。

## 文件

| 文件 | 作用 |
| --- | --- |
| `src/main/java/.../ServerApplication.java` | 入口，对应 Go 的 `main.go` |
| `.../controller/VersionController.java` | `GET /api/version` |
| `.../api/VersionResponse.java` | 响应体（Lombok `@Data`/`@Builder`） |
| `.../config/AppProperties.java` | 绑定 `app.*`，对应 Go 的 `config.json` |
| `src/main/resources/application.yml` | 端口 8791、只绑 127.0.0.1（Go 的复盘服务占 8790） |
| `src/main/resources/logback-spring.xml` | 控制台 + `logs/` 按天滚动，留 30 天 |

没有数据库、没有 JPA/MyBatis —— 数据仍是 Go 版写出的 CSV。
