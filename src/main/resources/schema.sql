-- ---------------------------------------------------------------------------
-- tradingview-j 业务库（SQLite）
--
-- 五条约定：
--   1. K 线不在这里。行情在 data/<folder>/<日期>.csv，本库只存「谁在什么时候做了什么」。
--   2. 表之间的引用一律是「按 id 引用」，不做 JPA 关联映射（没有 @ManyToOne）。
--      外键由下面的 REFERENCES 让数据库自己守（连接打开时 PRAGMA foreign_keys=ON）。
--      这样做的理由：聚合边界显式、加载不用 fetch join、不会踩 LazyInitializationException。
--   3. 金额/价格/数量是 TEXT。SQLite 没有 decimal 类型，NUMERIC 亲和性会把
--      DECIMAL 偷偷转成 REAL 丢精度；账目不能这么干，所以统一按字符串存，
--      由 BigDecimalTextConverter 在 Java 侧还原成 BigDecimal。
--      时间戳是 INTEGER，Unix 毫秒（InstantEpochConverter）。
--   4. **PF 账户是唯一的操作归属点，但不是数据的所有者**。
--      没有「会话」这张表了：挂单、成交、操作记录、机器人、交易记录
--      全部用 account_id 指向 prop_firm_account。
--      **账户不再是"一个标的"**：它是可以同时做 MES / MNQ / MGC 的考核账户，
--      所以凡是"挂在一根 K 线上"且**要记账**的数据（挂单、成交、交易记录、
--      机器人）都另有 symbol_id —— 只有 account_id 就没有时间轴，只有 symbol_id
--      就不知道算在谁头上，两个都要。
--      **图形（drawing）和语音（audio_clip）例外：它们没有 account_id。**
--      两者的附体是 K 线（symbol_id + bar_time），不参与任何账户统计 —— 既不进余额、
--      也不算回撤/一致性/盈利日。给它们挂账户只会让「同一条线换个账户就消失」
--      变成一种需要解释的行为。谁画的/谁录的仍有 created_by / actor 记着。
--      这些 account_id 列 **NOT NULL 且不加外键**：删账户只删 prop_firm_account 那一行，
--      子表里仍然保留原来那个 account_id 字符串（指向一个已经不存在的账户），既不置空也不删行。
--      为什么不加外键：SET NULL 会改写子表、CASCADE 会删数据、RESTRICT 会让账户
--      一旦有数据就永远删不掉 —— 三种都不是想要的。一致性由 Java 侧的
--      PropFirmAccountService#requireActive 把关，不用数据库的引用约束。
--      想删数据只能显式删那一行（图形尤其如此：只有用户自己点删除才该消失）。
--   5. 枚举列（actor / action / kind）存 **Java 常量名**（HUMAN / BAR_ADVANCED），
--      没有任何转换器参与。Go 协议里的小写下划线串（human / bar_advanced）由输出侧
--      按「常量名转小写」换算，库里不为客户端格式让步。
--
-- 全部 IF NOT EXISTS，可以每次启动重复执行。
-- ---------------------------------------------------------------------------

-- 标的：一个合约品种（也是 K 线 CSV 目录的锚）
CREATE TABLE IF NOT EXISTS symbol (
    id         TEXT PRIMARY KEY,     -- 标的代码本身，如 CME_MINI:MES1!
    folder     TEXT NOT NULL UNIQUE, -- data/ 下的目录名，K 线靠它定位
    tick       TEXT,                 -- 最小跳动
    tick_value TEXT,                 -- 一跳值多少美元
    commission TEXT NOT NULL,        -- 每手每边的手续费（美元）；0 = 免手续费，不允许 NULL
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- PF 模板：**一个阶段**的规则预设（EVAL 一条、FUNDED 一条，靠 next_template_id 串成链）。
-- 不再有 daily_loss_limit / min_trading_days / profit_split：
--   · 单日盈利硬顶取消，只保留一致性上限；
--   · EVAL 没有最少交易日；FUNDED 数的是"盈利日"，字段是 min_profit_days；
--   · 本项目是模拟考核，出金提利润没有意义，所以没有分成比例。
CREATE TABLE IF NOT EXISTS prop_firm_template (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    description          TEXT,
    stage                TEXT NOT NULL,  -- EVAL / FUNDED / PAYOUT：用这套模板开出来的账户处于哪个阶段
    next_template_id     TEXT,           -- 达标后接哪套模板；NULL = 升到尽头（PAYOUT）
    account_size         TEXT,           -- 名义规模 + 阶段起始余额（升级时余额重置成它）
    profit_target        TEXT,           -- NULL = 无盈利目标（FUNDED 就是 NULL）
    max_drawdown         TEXT,
    drawdown_type        TEXT,           -- 实际只用 TRAILING_EOD，留着是为了不把话说死
    trailing_lock_level  TEXT,           -- EOD 余额达到此值后亏损线冻结（50K Flex = 52100）
    consistency_ratio    TEXT,           -- 一致性上限（0.5）；NULL = 这套考核不要求一致性
    min_profit_days      INTEGER,        -- 需要几个"盈利日"（FUNDED = 5）；NULL = 不需要
    profit_day_threshold TEXT,           -- 单个盈利日的最低净盈利（150）
    position_tiers_json  TEXT,           -- [{"fromProfit":"0","maxContracts":20}, …]，净持仓手数上限
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

-- PF 账户：唯一的「操作归属点」，也是统计考核期一切操作的那个点。
-- 它是一个临时统计点，不是容器：删掉这一行，下面的子数据全部原地保留（account_id 原样留着）。
-- 规则在开出来那一刻从模板**复制**一份存进来（模板后续改动不影响已有账户）。
--
-- **阶段是逐行升级的，不是一次判定**：EVAL 达标 → 同一行切成 FUNDED（规则换成下一套、
-- base_balance 重置回账户规模）；FUNDED 攒够盈利日 → 切成 PAYOUT 并置 status=COMPLETED。
-- 每次切换在 prop_firm_stage_log 里留一行，所以"建户时就是 FUNDED"和"考过来的 FUNDED"
-- 在流水里只是行数不同，账户行长得一样。
--
-- **balance 不能由成交重算**：升级时余额被强制拨回账户规模，Σ净盈亏会算出一个负数。
-- 所以每个阶段存一个 base_balance，balance = base_balance + 本阶段净额。
CREATE TABLE IF NOT EXISTS prop_firm_account (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    template_id          TEXT NOT NULL,  -- 从哪套模板开的；模板删了也保留原 id（不置空）
    stage                TEXT NOT NULL,  -- 当前阶段：EVAL / FUNDED / PAYOUT
    -- ↓↓ 以下 9 列是建户那一刻从模板复制的规则快照，和模板同名同义
    account_size         TEXT,
    profit_target        TEXT,
    max_drawdown         TEXT,
    drawdown_type        TEXT,
    trailing_lock_level  TEXT,
    consistency_ratio    TEXT,
    min_profit_days      INTEGER,
    profit_day_threshold TEXT,
    position_tiers_json  TEXT,
    -- ↑↑
    base_balance         TEXT NOT NULL,  -- 本阶段起点余额（= account_size）
    eod_peak_balance     TEXT NOT NULL,  -- 本阶段"日终余额"的历史最高，初值 = base_balance
    balance              TEXT NOT NULL,  -- 当前余额 = base_balance + 本阶段已实现净额
    stage_started_at     INTEGER NOT NULL,
    status               TEXT NOT NULL,  -- ACTIVE / FAILED（走到 PAYOUT 用 stage 表达，不另立一个状态）
    started_at           INTEGER NOT NULL,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

-- 阶段流水：这个账户"当过哪几个阶段的什么身份"。
-- 建户时无条件写一行（reason=CREATED），所以建户即 FUNDED 也有记录。
-- account_id 按本文件第 4 条约定：不加外键、删账户也保留 —— 它是这个账户的生命记录。
CREATE TABLE IF NOT EXISTS prop_firm_stage_log (
    id               TEXT PRIMARY KEY,
    account_id       TEXT NOT NULL,
    seq              INTEGER NOT NULL,   -- 从 0 起，账户内连续
    stage            TEXT NOT NULL,
    started_at       INTEGER NOT NULL,
    ended_at         INTEGER,            -- NULL = 就是当前所处阶段
    base_balance     TEXT NOT NULL,
    balance_at_end   TEXT,               -- 离开那一刻的余额
    net_pnl          TEXT,               -- 该阶段净盈亏（balance_at_end - base_balance）
    max_daily_profit TEXT,               -- 该阶段单日最大净盈利，一致性就是拿它算的
    profit_days      INTEGER,            -- 该阶段达标盈利日数
    reason           TEXT NOT NULL,      -- 这一行是怎么来的：CREATED（建户即是此阶段）/ PROMOTED（从上一阶段升上来）
    end_reason       TEXT,               -- 这个阶段是怎么结束的：PROMOTED / FAILED / PAYOUT_DONE；NULL = 还开着
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_stage_account_seq ON prop_firm_stage_log (account_id, seq);

-- 手续费不单独建表：它是标的自己的一个参数（symbol.commission），
-- 每手每边多少美元，跟着品种走。以前那两张 commission_schedule / commission_rate
-- 已删除 —— 「一套全局口径 + 一张价目表」这层结构对一只标的只说一次就够了。
-- 这一列 **NOT NULL**：0 就是「免费」（NYMEX:CL1! 就是这样），没有第三个「还没配」状态。
-- 允许 NULL 的话，漏配的标的会安静地按免费结算，账面上看不出来。

-- 指标：暂时没有表。
-- 「指标怎么存」还没定（定义表 / 参数 JSON / 前端本地），在决定之前不建半张表。
-- 旧库里的 indicator 表要手动删：DROP TABLE indicator;（本脚本只管建，不管删）

-- 绘图图形：5 种图形共用「起点 + 终点」两个锚。
-- **不带 account_id**：图形依附在 K 线上（symbol_id + bar_time），不参与账户统计。
-- 只有用户手动删除才会消失：删账户跟它一点关系都没有（它压根没记着自己属于谁）。
CREATE TABLE IF NOT EXISTS drawing (
    id            TEXT PRIMARY KEY,
    symbol_id     TEXT NOT NULL REFERENCES symbol (id),  -- 画在哪个品种的时间轴上
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
    created_by    TEXT NOT NULL,     -- 谁画的（人/机器人），不是账户
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_drawing_symbol ON drawing (symbol_id);

-- 斐波那契的四条水平线（只有 FIB 图形有）。
-- 这一条是**真级联**：水平线是图形的一部分，图形没了它就没有意义。
CREATE TABLE IF NOT EXISTS drawing_fib_level (
    drawing_id  TEXT NOT NULL REFERENCES drawing (id) ON DELETE CASCADE,
    level_index INTEGER NOT NULL,
    ratio       TEXT NOT NULL,
    color       TEXT,
    PRIMARY KEY (drawing_id, level_index)
);

-- 录音：只存元数据，WAV 文件在磁盘上（storage 层管路径）。
-- **不带 account_id**：语音依附在 K 线上（symbol_id + bar_time），不参与账户统计。
CREATE TABLE IF NOT EXISTS audio_clip (
    id          TEXT PRIMARY KEY,
    symbol_id   TEXT NOT NULL REFERENCES symbol (id),  -- 讲的是哪个品种的 K 线
    bar_time    TEXT NOT NULL,       -- 挂在哪根 K 线上
    seq         INTEGER NOT NULL,    -- 同一根上的第几段（上限 9）
    file_name   TEXT NOT NULL,       -- 相对路径，不存绝对路径
    duration_ms INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL,
    actor       TEXT,                -- JAVA 常量名：HUMAN / BOT / SYSTEM
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audio_symbol_bar ON audio_clip (symbol_id, bar_time);

-- 交易记录：一笔完整交易（开仓前那一屏 + 之后每一步操作）。
-- 名字不叫 recording：它确实不是录像，是「操作流水 + 结算摘要」。
-- account_id 是统计归属，不是所有权：账户删了记录还在，account_id 原样留着。
CREATE TABLE IF NOT EXISTS trade_record (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,    -- 保留原账户 id；账户删了也不置空
    symbol_id      TEXT NOT NULL REFERENCES symbol (id),  -- 这笔交易做的是哪个品种
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
    actor          TEXT NOT NULL,    -- JAVA 常量名：HUMAN / BOT（SYSTEM 不是一笔交易的来源）
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
    kind          TEXT NOT NULL,     -- JAVA 常量名：BASE / BAR_ADVANCED / RECORDING_AUDIO …（见 ReplayAction）
    bar_time      TEXT NOT NULL,
    cursor_index  INTEGER NOT NULL,
    actor         TEXT,              -- JAVA 常量名：HUMAN / BOT / SYSTEM
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
    account_id    TEXT NOT NULL,     -- 保留原账户 id；账户删了也不置空
    symbol_id     TEXT NOT NULL REFERENCES symbol (id),  -- 挂在哪个品种上
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
-- 成交是盘面上的事实：账户删了，成交还在，account_id 原样留着。
CREATE TABLE IF NOT EXISTS trade (
    id              TEXT PRIMARY KEY,
    account_id      TEXT NOT NULL,   -- 保留原账户 id；账户删了也不置空
    symbol_id       TEXT NOT NULL REFERENCES symbol (id),  -- 成交在哪个品种上
    order_id        TEXT NOT NULL,
    bar_time        TEXT NOT NULL,
    side            TEXT NOT NULL,
    price           TEXT NOT NULL,
    quantity        TEXT NOT NULL,
    realized_pnl    TEXT NOT NULL,   -- 平均成本法算出的「这一笔平掉的部分」（**税前毛利**）
    commission      TEXT NOT NULL,   -- 这一笔成交的单边手续费（成交那一刻的快照，改费率不影响历史账）
    trade_record_id TEXT REFERENCES trade_record (id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trade_account ON trade (account_id, symbol_id, bar_time);
CREATE INDEX IF NOT EXISTS idx_trade_record ON trade (trade_record_id);

-- 操作记录：append-only 的审计流水。
-- account_id **NOT NULL 且故意不加外键**：任何改写（包括 SET NULL）都会让这条流水
-- 不再是「当时发生的事实」。账户删了这里仍保留原 id，指向一个不存在的账户不丢信息。
CREATE TABLE IF NOT EXISTS audit_event (
    id           TEXT PRIMARY KEY,
    account_id   TEXT NOT NULL,      -- 保留原账户 id，永不改写/置空
    seq          INTEGER NOT NULL,
    actor        TEXT NOT NULL,      -- JAVA 常量名：HUMAN / BOT / SYSTEM
    action       TEXT NOT NULL,      -- JAVA 常量名：BAR_ADVANCED / ORDER_PLACED …（见 ReplayAction）
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
    account_id  TEXT NOT NULL,       -- 保留原账户 id；账户删了也不置空
    symbol_id   TEXT NOT NULL REFERENCES symbol (id),  -- 这个机器人在哪个品种上跑
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
