package com.tradingviewj.server.storage;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.tradingviewj.server.config.AppProperties;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Component;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.List;
import java.util.Map;

/**
 * 读 {@code <bar-root>/_symbols.json} —— 标的清单的<strong>唯一来源</strong>。
 *
 * <h2>为什么是读这个文件，而不是再配一份</h2>
 *
 * <p>这份文件是<strong>抓取侧（Go 程序）写的</strong>：它决定了 {@code data/} 下有哪些目录、
 * 每个目录是什么标的、一跳值多少、手续费多少。它已经在事实发生地维护这些字段了
 * （见 {@code symRec.Commission}），Spring 侧再抄一份 YAML 只会让两边随时间漂移。
 *
 * <p>所以这里的定位是<strong>只读的搬运工</strong>：文件怎么写，库就怎么有。要改标的，
 * 改抓取侧的配置，重启服务即可。
 *
 * <h2>为什么「没写手续费」的标的直接丢掉</h2>
 *
 * <p>{@code symbol.commission} 是 {@code NOT NULL}，只有两态：0 = 免费、&gt;0 = 收费。
 * 「不知道多少」不是一个可以入库的状态 —— 让它带着 {@code null} 进去，会在算盈亏的时候
 * 被当成 0，静默少算成本。所以宁可不进库，并在日志里点名。
 *
 * <h2>文件不存在 ≠ 出错</h2>
 *
 * <p>新环境、还没抓过行情的时候本来就没有这个文件。返回空清单，由调用方决定怎么说
 * （开账户时会得到一句「标的表为空」）。但文件<strong>存在却解析不了</strong>是坏数据，
 * 当场抛 —— 静默当成空清单，就会退回成「开了户但不知道为什么不行」。
 */
@Slf4j
@Component
public class SymbolCatalog {

    /** 清单文件名，和 Go 侧 {@code config.json} 的 {@code outDir} 并列。 */
    public static final String FILE_NAME = "_symbols.json";

    private final Path root;
    private final ObjectMapper mapper = new ObjectMapper();

    public SymbolCatalog(AppProperties properties) {
        this.root = Path.of(properties.getBarRoot()).toAbsolutePath().normalize();
    }

    /** 清单文件的位置（日志里点名用）。 */
    public Path file() {
        return root.resolve(FILE_NAME);
    }

    /**
     * 读出全部标的。
     *
     * @return 按清单里的书写顺序；文件不存在时是空列表
     * @throws UncheckedIOException 文件存在但读不了或不是合法 JSON
     */
    public List<SymbolSpec> read() {
        Path file = file();
        if (!Files.isRegularFile(file)) {
            log.info("标的清单不存在，跳过同步: {}", file);
            return List.of();
        }

        JsonNode root;
        try {
            root = mapper.readTree(Files.readString(file, StandardCharsets.UTF_8));
        } catch (IOException e) {
            throw new UncheckedIOException("标的清单解析失败: " + file, e);
        }

        // {"version":1,"symbols":{"CME_MINI:MES1!":{"folder":"...",...}}}
        JsonNode symbols = root.path("symbols");
        if (!symbols.isObject()) {
            log.warn("标的清单里没有 symbols 对象: {}", file);
            return List.of();
        }

        List<SymbolSpec> specs = new ArrayList<>();
        Iterator<Map.Entry<String, JsonNode>> it = symbols.fields();
        while (it.hasNext()) {
            Map.Entry<String, JsonNode> entry = it.next();
            String code = entry.getKey();
            JsonNode node = entry.getValue();

            String folder = text(node, "folder");
            if (folder == null) {
                log.warn("标的 {} 没写 folder，跳过：没有目录就读不到它的 K 线", code);
                continue;
            }

            BigDecimal commission = decimal(node, "commission");
            if (commission == null) {
                log.warn("标的 {} 没写 commission，跳过：symbol.commission 不允许为空（免费请写 0）", code);
                continue;
            }

            specs.add(new SymbolSpec(code, folder,
                    decimal(node, "tick"), decimal(node, "tickValue"), commission));
        }
        return specs;
    }

    /** 取字符串字段；缺失、null、空白都返回 {@code null}。 */
    private static String text(JsonNode node, String field) {
        JsonNode value = node.path(field);
        if (value.isMissingNode() || value.isNull()) {
            return null;
        }
        String text = value.asText().trim();
        return text.isEmpty() ? null : text;
    }

    /**
     * 取金额字段；缺失、null、非数字都返回 {@code null}。
     *
     * <p>Go 侧用 {@code *float64} 序列化，所以正常是 JSON 数字；写成字符串也认。
     * 解析不了时不抛：单个标的的手续费写错不该让整个清单读不出来，交给上层按
     * 「没写」处理（跳过 + 日志点名）。
     */
    private static BigDecimal decimal(JsonNode node, String field) {
        JsonNode value = node.path(field);
        if (value.isMissingNode() || value.isNull()) {
            return null;
        }
        if (value.isNumber()) {
            return value.decimalValue();
        }
        String text = value.asText().trim();
        if (text.isEmpty()) {
            return null;
        }
        try {
            return new BigDecimal(text);
        } catch (NumberFormatException e) {
            log.warn("字段 {} 不是数字: {}", field, text);
            return null;
        }
    }
}
