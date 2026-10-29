-- ---------------------------------------------------------------------------
-- tradingview-j 业务库（SQLite）
--
-- 三条约定：
--   1. K 线不在这里。行情在 data/<folder>/<日期>.csv，本库只存「谁在什么时候做了什么」。
--   2. 表之间的引用一律是「按 id 引用」，不做 JPA 关联映射（没有 @ManyToOne）。
--      外键由下面的 REFERENCES 让数据库自己守（连接打开时 PRAGMA foreign_keys=ON）。
--      这样做的理由：聚合边界显式、加载不用 fetch join、不会踩 LazyInitializationException。
--   3. 金额/价格/数量是 TEXT。SQLite 没有 decimal 类型，NUMERIC 亲和性会把
--      DECIMAL 偷偷转成 REAL 丢精度；账目不能这么干，所以统一按字符串存，
--      由 BigDecimalTextConverter 在 Java 侧还原成 BigDecimal。
--      时间戳是 INTEGER，Unix 毫秒（InstantEpochConverter）。
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

-- 复盘会话：一个标的 + 一个锚点 = 一块盘面
CREATE TABLE IF NOT EXISTS replay_session (
    id             TEXT PRIMARY KEY,
    symbol_id      TEXT NOT NULL REFERENCES symbol (id),
    anchor_bar_time TEXT NOT NULL,   -- 锚点 K 线的时间明文，如 2026-10-16 12:00
    cursor_index   INTEGER NOT NULL, -- 已经揭示到第几根
    operator_name  TEXT,             -- 当前接管的人，NULL = 没人
    created_by     TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_symbol ON replay_session (symbol_id);

-- 指标定义（值不入库：拿 OHLC 现算，口径只有一份）
CREATE TABLE IF NOT EXISTS indicator (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
    type        TEXT NOT NULL,       -- EMA / SMA / RMA / WMA / ATR
    period      INTEGER NOT NULL,
    source      TEXT NOT NULL,       -- CLOSE / HIGH / LOW / HL2
    params_json TEXT,                -- 其余参数，前端自定义
    visible     INTEGER NOT NULL DEFAULT 1,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_indicator_session ON indicator (session_id);

-- 绘图图形：5 种图形共用「起点 + 终点」两个锚
CREATE TABLE IF NOT EXISTS drawing (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
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
CREATE INDEX IF NOT EXISTS idx_drawing_session ON drawing (session_id);

-- 斐波那契的四条水平线（只有 FIB 图形有）
CREATE TABLE IF NOT EXISTS drawing_fib_level (
    drawing_id  TEXT NOT NULL REFERENCES drawing (id) ON DELETE CASCADE,
    level_index INTEGER NOT NULL,
    ratio       TEXT NOT NULL,
    color       TEXT,
    PRIMARY KEY (drawing_id, level_index)
);

-- 挂单。表名不能叫 order（SQL 保留字，SQLite 会直接报错）
CREATE TABLE IF NOT EXISTS trade_order (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
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
CREATE INDEX IF NOT EXISTS idx_order_session ON trade_order (session_id);

-- 成交。持仓、已实现盈亏都是这张表的**派生结果**，不单独落库
CREATE TABLE IF NOT EXISTS trade (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
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
CREATE INDEX IF NOT EXISTS idx_trade_session ON trade (session_id, bar_time);
CREATE INDEX IF NOT EXISTS idx_trade_record ON trade (trade_record_id);

-- 录音：只存元数据，WAV 文件在磁盘上（storage 层管路径）
CREATE TABLE IF NOT EXISTS audio_clip (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
    bar_time    TEXT NOT NULL,       -- 挂在哪根 K 线上
    seq         INTEGER NOT NULL,    -- 同一根上的第几段（上限 9）
    file_name   TEXT NOT NULL,       -- 相对路径，不存绝对路径
    duration_ms INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL,
    recorded_by TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audio_session ON audio_clip (session_id, bar_time);

-- 操作记录：append-only 的审计流水
CREATE TABLE IF NOT EXISTS audit_event (
    id           TEXT PRIMARY KEY,
    session_id   TEXT REFERENCES replay_session (id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    actor        TEXT NOT NULL,      -- human / bot / system
    action       TEXT NOT NULL,      -- bar_advanced / order_placed / ...
    payload_json TEXT,               -- 原始广播负载，原样留存
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_audit_session_seq ON audit_event (session_id, seq);

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
    session_id  TEXT NOT NULL REFERENCES replay_session (id) ON DELETE CASCADE,
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
CREATE INDEX IF NOT EXISTS idx_bot_session ON strategy_bot (session_id);

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

-- PF 账户：开出来那一刻，规则从模板**复制**一份存进来（模板后续改动不影响已有账户）
CREATE TABLE IF NOT EXISTS prop_firm_account (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
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

-- 交易记录：一笔完整交易（开仓前那一屏 + 之后每一步操作）。
-- 名字不叫 recording：它确实不是录像，是「操作流水 + 结算摘要」。
CREATE TABLE IF NOT EXISTS trade_record (
    id             TEXT PRIMARY KEY,
    session_id     TEXT REFERENCES replay_session (id) ON DELETE SET NULL,
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
    source         TEXT NOT NULL,    -- HUMAN / BOT
    source_name    TEXT,
    ended_at       INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_record_session ON trade_record (session_id);
CREATE INDEX IF NOT EXISTS idx_record_account ON trade_record (account_id);

-- 每一步操作。和数据表分开是因为它可能上千行，只有打开回放时才需要
CREATE TABLE IF NOT EXISTS trade_record_step (
    id            TEXT PRIMARY KEY,
    record_id     TEXT NOT NULL REFERENCES trade_record (id) ON DELETE CASCADE,
    step_no       INTEGER NOT NULL,  -- 0 = 起点（开仓前那一屏）
    kind          TEXT NOT NULL,     -- base / 动作名 / recording_audio
    bar_time      TEXT NOT NULL,
    cursor_index  INTEGER NOT NULL,
    actor         TEXT,
    audio_clip_id TEXT REFERENCES audio_clip (id) ON DELETE SET NULL,
    payload_json  TEXT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_step_record_no ON trade_record_step (record_id, step_no);

-- 用户给交易记录打的自定义标签
CREATE TABLE IF NOT EXISTS trade_record_tag (
    record_id  TEXT NOT NULL REFERENCES trade_record (id) ON DELETE CASCADE,
    tag_index  INTEGER NOT NULL,
    name       TEXT NOT NULL,
    color      TEXT,                 -- #rrggbb
    PRIMARY KEY (record_id, tag_index)
);
