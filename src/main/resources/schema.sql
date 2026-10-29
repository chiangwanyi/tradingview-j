-- ---------------------------------------------------------------------------
-- tradingview-j 业务库（SQLite）
--
-- 四条约定：
--   1. K 线不在这里。行情在 data/<folder>/<日期>.csv，本库只存「谁在什么时候做了什么」。
--   2. 表之间的引用一律是「按 id 引用」，不做 JPA 关联映射（没有 @ManyToOne）。
--      外键由下面的 REFERENCES 让数据库自己守（连接打开时 PRAGMA foreign_keys=ON）。
--      这样做的理由：聚合边界显式、加载不用 fetch join、不会踩 LazyInitializationException。
--   3. 金额/价格/数量是 TEXT。SQLite 没有 decimal 类型，NUMERIC 亲和性会把
--      DECIMAL 偷偷转成 REAL 丢精度；账目不能这么干，所以统一按字符串存，
--      由 BigDecimalTextConverter 在 Java 侧还原成 BigDecimal。
--      时间戳是 INTEGER，Unix 毫秒（InstantEpochConverter）。
--   4. **PF 账户是唯一的操作归属点，但不是数据的所有者**。
--      没有「会话」这张表了：图形、挂单、成交、语音、操作记录、机器人、交易记录、
--      指标全部用 account_id 指向 prop_firm_account。指向关系是
--      ON DELETE SET NULL —— 删账户只清空归属，一行子数据都不会被删。
--      想删数据只能显式删那一行（图形尤其如此：只有用户自己点删除才该消失）。
--      代价：这些 account_id 列必须可空。「创建时必须有账户」由 Java 侧工厂方法把关，
--      因为列上的 NOT NULL 和 ON DELETE SET NULL 不可能同时成立。
--
-- 全部 IF NOT EXISTS，可以每次启动重复执行。
-- ---------------------------------------------------------------------------

-- 标的：一个合约品种（也是 K 线 CSV 目录的锚）
CREATE TABLE IF NOT EXISTS symbol (
    id         TEXT PRIMARY KEY,     -- 标的代码本身，如 CME_MINI:MES1!
    folder     TEXT NOT NULL UNIQUE, -- data/ 下的目录名，K 线靠它定位
    tick       TEXT,                 -- 最小跳动
    tick_value TEXT,                 -- 一跳值多少美元
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- PF 模板：考核规则的预设
CREATE TABLE IF NOT EXISTS prop_firm_template (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    description      TEXT,
    account_size     TEXT,
    profit_target    TEXT,
    max_drawdown     TEXT,
    daily_loss_limit TEXT,
    drawdown_type    TEXT,           -- STATIC / TRAILING_INTRADAY / TRAILING_EOD
    min_trading_days INTEGER,
    profit_split     TEXT,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

-- PF 账户：唯一的「操作归属点」，也是统计考核期一切操作的那个点。
-- 它是一个临时统计点，不是容器：删掉这一行，下面的子数据全部原地保留（account_id 置空）。
-- 规则在开出来那一刻从模板**复制**一份存进来（模板后续改动不影响已有账户）。
CREATE TABLE IF NOT EXISTS prop_firm_account (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    symbol_id        TEXT NOT NULL REFERENCES symbol (id),  -- 这个账户做哪个标的
    template_id      TEXT REFERENCES prop_firm_template (id) ON DELETE SET NULL,
    account_size     TEXT,
    profit_target    TEXT,
    max_drawdown     TEXT,
    daily_loss_limit TEXT,
    drawdown_type    TEXT,
    min_trading_days INTEGER,
    profit_split     TEXT,
    balance          TEXT NOT NULL,
    peak_equity      TEXT NOT NULL,
    trading_days     INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL,  -- ACTIVE / PASSED / FAILED
    started_at       INTEGER NOT NULL,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_account_symbol ON prop_firm_account (symbol_id);

-- 手续费价目表：全库只有一行（id 固定为 'commission'）。
-- 口径（type）只有一份，费率（commission_rate）按品种根名查——
-- 这和 data/_symbols.json 里 commission 段的结构一一对应。
CREATE TABLE IF NOT EXISTS commission_schedule (
    id         TEXT PRIMARY KEY,     -- 固定 'commission'
    type       TEXT NOT NULL,        -- PER_CONTRACT_PER_SIDE
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- 每手每边的手续费（美元）。键是品种根名（MES / MNQ / MGC），不是完整标的代码
CREATE TABLE IF NOT EXISTS commission_rate (
    schedule_id TEXT NOT NULL REFERENCES commission_schedule (id) ON DELETE CASCADE,
    symbol_root TEXT NOT NULL,
    rate        TEXT NOT NULL,       -- 金额一律 TEXT，走 BigDecimalTextConverter
    PRIMARY KEY (schedule_id, symbol_root)
);

-- 指标定义（值不入库：拿 OHLC 现算，口径只有一份）
CREATE TABLE IF NOT EXISTS indicator (
    id          TEXT PRIMARY KEY,
    account_id  TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    type        TEXT NOT NULL,       -- EMA / SMA / RMA / WMA / ATR
    period      INTEGER NOT NULL,
    source      TEXT NOT NULL,       -- CLOSE / HIGH / LOW / HL2
    params_json TEXT,                -- 其余参数，前端自定义
    visible     INTEGER NOT NULL DEFAULT 1,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_indicator_account ON indicator (account_id);

-- 绘图图形：5 种图形共用「起点 + 终点」两个锚。
-- 只有用户手动删除才会消失：删账户不删图形，account_id 置空而已。
CREATE TABLE IF NOT EXISTS drawing (
    id            TEXT PRIMARY KEY,
    account_id    TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    kind          TEXT NOT NULL,     -- TREND / RAY / RECT / ARROW / FIB
    start_bar_time TEXT NOT NULL,
    start_price   TEXT NOT NULL,
    start_offset  INTEGER NOT NULL DEFAULT 0,  -- 从锚点向右延伸的根数
    end_bar_time  TEXT NOT NULL,
    end_price     TEXT NOT NULL,
    end_offset    INTEGER NOT NULL DEFAULT 0,
    color         TEXT,
    width         TEXT,
    dash          TEXT,              -- SOLID / DASHED
    fill          TEXT,
    fill_opacity  TEXT,
    snapped       INTEGER NOT NULL DEFAULT 0,
    created_by    TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_drawing_account ON drawing (account_id);

-- 斐波那契的四条水平线（只有 FIB 图形有）。
-- 这一条是**真级联**：水平线是图形的一部分，图形没了它就没有意义。
CREATE TABLE IF NOT EXISTS drawing_fib_level (
    drawing_id  TEXT NOT NULL REFERENCES drawing (id) ON DELETE CASCADE,
    level_index INTEGER NOT NULL,
    ratio       TEXT NOT NULL,
    color       TEXT,
    PRIMARY KEY (drawing_id, level_index)
);

-- 录音：只存元数据，WAV 文件在磁盘上（storage 层管路径）
CREATE TABLE IF NOT EXISTS audio_clip (
    id          TEXT PRIMARY KEY,
    account_id  TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    bar_time    TEXT NOT NULL,       -- 挂在哪根 K 线上
    seq         INTEGER NOT NULL,    -- 同一根上的第几段（上限 9）
    file_name   TEXT NOT NULL,       -- 相对路径，不存绝对路径
    duration_ms INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL,
    actor       TEXT,                -- human / bot / system
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audio_account ON audio_clip (account_id, bar_time);

-- 交易记录：一笔完整交易（开仓前那一屏 + 之后每一步操作）。
-- 名字不叫 recording：它确实不是录像，是「操作流水 + 结算摘要」。
-- account_id 是统计归属，不是所有权：账户删了记录还在。
CREATE TABLE IF NOT EXISTS trade_record (
    id             TEXT PRIMARY KEY,
    account_id     TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    anchor_bar_time TEXT NOT NULL,
    entry_bar_time TEXT,
    exit_bar_time  TEXT,
    side           TEXT,             -- LONG / SHORT
    quantity       TEXT,
    max_quantity   TEXT,
    entry_price    TEXT,
    exit_price     TEXT,
    realized_pnl   TEXT,
    mfe            TEXT,             -- 最大有利波动
    mae            TEXT,             -- 最大不利波动
    bars_held      INTEGER NOT NULL DEFAULT 0,
    fill_count     INTEGER NOT NULL DEFAULT 0,
    closed         INTEGER NOT NULL DEFAULT 0,
    actor          TEXT NOT NULL,    -- human / bot（SYSTEM 不是一笔交易的来源）
    actor_name     TEXT,
    ended_at       INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_record_account ON trade_record (account_id);

-- 每一步操作。和数据表分开是因为它可能上千行，只有打开回放时才需要。
-- 步骤是记录的一部分 → 记录没了步骤也该没，这一条是**真级联**。
CREATE TABLE IF NOT EXISTS trade_record_step (
    id            TEXT PRIMARY KEY,
    record_id     TEXT NOT NULL REFERENCES trade_record (id) ON DELETE CASCADE,
    step_no       INTEGER NOT NULL,  -- 0 = 起点（开仓前那一屏）
    kind          TEXT NOT NULL,     -- base / 动作名 / recording_audio（见 ReplayAction）
    bar_time      TEXT NOT NULL,
    cursor_index  INTEGER NOT NULL,
    actor         TEXT,              -- human / bot / system
    audio_clip_id TEXT REFERENCES audio_clip (id) ON DELETE SET NULL,
    payload_json  TEXT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_step_record_no ON trade_record_step (record_id, step_no);

-- 用户给交易记录打的自定义标签（标签是记录的一部分 → 真级联）
CREATE TABLE IF NOT EXISTS trade_record_tag (
    record_id  TEXT NOT NULL REFERENCES trade_record (id) ON DELETE CASCADE,
    tag_index  INTEGER NOT NULL,
    name       TEXT NOT NULL,
    color      TEXT,                 -- #rrggbb
    PRIMARY KEY (record_id, tag_index)
);

-- 挂单。表名不能叫 order（SQL 保留字，SQLite 会直接报错）
CREATE TABLE IF NOT EXISTS trade_order (
    id            TEXT PRIMARY KEY,
    account_id    TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    side          TEXT NOT NULL,     -- BUY / SELL
    type          TEXT NOT NULL,     -- LIMIT / STOP / MARKET
    status        TEXT NOT NULL,     -- OPEN / FILLED / CANCELLED
    price         TEXT NOT NULL,
    quantity      TEXT NOT NULL,
    placed_at_bar TEXT NOT NULL,
    placed_index  INTEGER NOT NULL,
    filled_at_bar TEXT,
    filled_index  INTEGER NOT NULL DEFAULT -1,  -- -1 = 没成交
    fill_price    TEXT,
    oco_group     TEXT,              -- 同一组最多成交一张；NULL = 独立挂单
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_order_account ON trade_order (account_id);

-- 成交。持仓、已实现盈亏都是这张表的**派生结果**，不单独落库。
-- 成交是盘面上的事实：账户删了，成交还在。
CREATE TABLE IF NOT EXISTS trade (
    id              TEXT PRIMARY KEY,
    account_id      TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    order_id        TEXT NOT NULL,
    bar_time        TEXT NOT NULL,
    side            TEXT NOT NULL,
    price           TEXT NOT NULL,
    quantity        TEXT NOT NULL,
    realized_pnl    TEXT NOT NULL,   -- 平均成本法算出的「这一笔平掉的部分」
    trade_record_id TEXT REFERENCES trade_record (id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trade_account ON trade (account_id, bar_time);
CREATE INDEX IF NOT EXISTS idx_trade_record ON trade (trade_record_id);

-- 操作记录：append-only 的审计流水。
-- account_id **故意不加外键**：ON DELETE SET NULL 会把一条审计流水改掉，而审计的
-- 全部价值就在于它没被改过。账户删了这里仍保留原 id，指向一个不存在的账户不丢信息。
CREATE TABLE IF NOT EXISTS audit_event (
    id           TEXT PRIMARY KEY,
    account_id   TEXT,
    seq          INTEGER NOT NULL,
    actor        TEXT NOT NULL,      -- human / bot / system
    action       TEXT NOT NULL,      -- bar_advanced / order_placed / ...（见 ReplayAction）
    payload_json TEXT,               -- 原始广播负载，原样留存
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_audit_account_seq ON audit_event (account_id, seq);

-- 策略：注册表。真正的算法是 strategy 包里的 Java 类，这张表只记「有哪些 + 参数」
CREATE TABLE IF NOT EXISTS strategy (
    id          TEXT PRIMARY KEY,
    code        TEXT NOT NULL UNIQUE, -- 对应 TradingStrategy 实现类的 bean 名
    name        TEXT NOT NULL,
    summary     TEXT,
    params_json TEXT,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- 策略执行机器人：一次运行 = 一行。运行期的逐拍状态不落库
CREATE TABLE IF NOT EXISTS strategy_bot (
    id          TEXT PRIMARY KEY,
    account_id  TEXT REFERENCES prop_firm_account (id) ON DELETE SET NULL,
    strategy_id TEXT REFERENCES strategy (id),  -- NULL = 老式随机演示机器人
    quantity    TEXT NOT NULL,
    interval_ms INTEGER NOT NULL,
    status      TEXT NOT NULL,       -- RUNNING / STOPPED / FINISHED / FAILED
    started_at  INTEGER NOT NULL,
    stopped_at  INTEGER,
    entry_count INTEGER NOT NULL DEFAULT 0,
    stop_reason TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bot_account ON strategy_bot (account_id);
