package replay

import "encoding/json"

// 前后端之间唯一的通信格式：JSON 文本帧，全部经 WebSocket（见 server.go / ws.go）。
//
// 两条刻意的规则：
//
//  1. **字段名写全，不做缩写。** 这个协议是要给人看的（排查问题、写机器人、录演示），
//     `quantity` 比 `qty` 多打几个字，但读日志时不用查表。
//  2. **上行只用一个 Request 结构体。** 请求类型有十几种，各自定义一个结构体再
//     反射分发，收益只在「类型安全」，代价是新增一种请求要改三处。这里统一用
//     Type 区分，只读该类型用得到的字段，其余留零值 —— 简单、好加。
//
// 时间一律用 CSV 里那一列的原始文本（"2026-10-16 12:00"），**不用时间戳、不换算时区**：
// 它是 K 线在协议里的唯一标识，也是标注点的横坐标。

// ---------------- 上行：前端 -> 后端 ----------------

// 请求类型常量。字面量就写在这里，前端 types/protocol.ts 里有同名的一份。
const (
	ReqHello          = "hello"
	ReqListSymbols    = "list_symbols"
	ReqListInstances  = "list_instances"
	ReqCreate         = "create_instance"
	ReqJoin           = "join_instance"
	ReqLeave          = "leave_instance"
	ReqDelete         = "delete_instance"
	ReqTakeControl    = "take_control"
	ReqRelease        = "release_control"
	ReqStep           = "step"
	ReqAnnAdd         = "annotation_add"
	ReqAnnMove        = "annotation_move"
	ReqAnnStyle       = "annotation_style" // 改已画好的图形的样式（换模板）：不动端点
	ReqAnnDelete      = "annotation_delete"
	ReqAnnClear       = "annotation_clear" // 一次清掉当前实例的全部绘图（画布右键菜单「移除 N 个绘图」）
	ReqListTemplates  = "list_templates"
	ReqSaveTemplate   = "save_template"
	ReqDelTemplate    = "delete_template"
	ReqOrderPlace     = "order_place"
	ReqOrderMarket    = "order_market"
	ReqOrderModify    = "order_modify" // 改挂单价：画布上拖动挂单线走这条
	ReqOrderCancel    = "order_cancel"
	ReqOrderFlatAll   = "order_flat_all" // 市价退仓 + 撤销全部挂单：一次动作换一条广播
	ReqBotStart       = "bot_start"
	ReqBotStop        = "bot_stop"
	ReqListStrategies = "list_strategies" // 有哪些策略可加载（启动面板用）
	ReqPing           = "ping"

	// 交易记录（v0.18.0 起是**自动**录制的：开仓即开始、平仓即结束，没有开关）。
	//
	// 两个旧的手动开关仍然认，但只回一句「已经是自动的了」——
	// 删掉它们会让老前端/老脚本收到「不认识的消息类型」，看不出到底发生了什么。
	ReqRecordStart = "record_start"
	ReqRecordStop  = "record_stop"
	// 【兼容别名】这两个在 v0.16.0 之后就等于 voice_add / voice_clear，
	// v0.18.0 起彻底并成一条路（不再要求「正在录制」—— 那个状态已经不存在了）。
	ReqRecordAudio       = "record_audio"
	ReqRecordAudioDelete = "record_audio_delete"
	// 下面三个是交易记录列表/详情/删除。**消息名保持原样**（list_recordings 等）：
	// 它们是线协议上的字面量，改名要两边同时发版，而「录像」只是这一代叫法变了。
	ReqListRecordings  = "list_recordings"
	ReqGetRecording    = "get_recording"
	ReqDeleteRecording = "delete_recording"
	// ReqSetRecordingTags 整份替换一笔交易的标签（v0.21.0）。
	//
	// 为什么是「整份替换」而不是 add_tag / remove_tag 两条：标签是**一组**，
	// 界面上一次操作要么加上一个、要么去掉一个，但真正要落盘的永远是这个集合的
	// 新样子。整份替换让服务端只有一条写路径，也就不存在「加和删并发之后
	// 两边各留一半」的中间态。
	ReqSetRecordingTags = "set_recording_tags"

	// 语音讲解（v0.16.0）：从录制里解耦的一级功能，**不必在录制中**。
	//
	// 为什么不是继续用 record_audio：那个名字和字段（record_*）把「录音」钉在了
	// 「录制」这件事上 —— 名字一改，前端、协议、测试三处才能各说各的。旧的
	// record_audio / record_audio_delete 留着当兼容别名（老脚本、老前端照样能用），
	// 但它们**要求的仍然是在录制中**，语义没有变。
	ReqVoiceAdd   = "voice_add"   // 一段语音讲解，挂在当前那根 K 线上（限制仍然是每根 9 条）
	ReqVoiceClear = "voice_clear" // 清空当前这根 K 线上的全部语音
)

// Request 是一条上行消息。每种类型只读自己那几个字段。
type Request struct {
	Type string `json:"type"`

	// hello
	Name string `json:"name"`

	// create_instance
	Symbol string `json:"symbol"`
	Time   string `json:"time"` // 空 = 由后端随机挑一个时间点
	// Latest = true 表示锚点取「数据目录里最新的那一根 K 线」（界面上的【新增实例（最新）】），
	// 这时 Time 和 Seed 都不起作用：用户不想（也不必）知道那个时间是什么。
	//
	// 为什么不在前端算好时间传过来：最新一根的时刻只存在于 CSV 内容里，文件名只到「哪一天」。
	// 让前端拼一个 "最后一天 + 某个钟点" 出来，遇到那天提前收市就会指向一根不存在的 K 线。
	Latest      bool  `json:"latest"`
	Bars        int   `json:"bars"`        // 画布上初始显示多少根（含锚点之前的历史），0 = 默认
	ForwardDays int   `json:"forwardDays"` // 锚点之后预加载几天，0 = 默认
	Seed        int64 `json:"seed"`        // 非 0 时随机锚点可复现（测试/复现问题用）

	// 所有和某个实例有关的请求
	InstanceID string `json:"instanceId"`

	// annotation_add / annotation_move
	Kind  string           `json:"kind"` // trend | ray | rect | arrow | fib
	Start *Point           `json:"start"`
	End   *Point           `json:"end"`
	Style *AnnotationStyle `json:"style"` // annotation_add：画下去那一刻的快照；annotation_style：换上的新样式
	Snap  bool             `json:"snap"`  // true = 端点吸附到该 K 线的 OHLC（前端按住 Ctrl）

	// annotation_move / annotation_delete / annotation_style
	AnnotationID string `json:"annotationId"`

	// save_template
	Template *Template `json:"template"`
	// delete_template
	TemplateID string `json:"templateId"`

	// order_place / order_market / bot_start
	// bot_start 用 Quantity 当「策略每笔下多少手」，所以这条请求也要校验它。
	Side     string  `json:"side"` // buy | sell
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`

	// order_cancel / order_modify
	OrderID string `json:"orderId"`

	// bot_start
	//
	// Strategy 空 = 原来的「随机演示机器人」：只会步进、乱画线、乱下单，
	// 用来验证链路。填了策略 id 就是策略机器人，每根 K 线问策略一次该不该开仓。
	IntervalMs int                `json:"intervalMs"`
	Strategy   string             `json:"strategy"`
	Params     map[string]float64 `json:"params"` // 策略参数覆盖；缺省用策略自己的默认值

	// record_audio：一段 WAV 的 base64。**不走文件上传** —— 录音只有几十 KB
	// 到几百 KB，和别的动作一样走这条有序的消息流，顺序天然和操作对得上。
	AudioData  string `json:"audioData"`
	DurationMs int    `json:"durationMs"`

	// get_recording / delete_recording
	RecordingID string `json:"recordingId"`

	// set_recording_tags：这笔交易**之后**应该有的那一组标签（空数组 = 全部清掉）。
	// 颜色和名字都由用户定，服务端只做清洗（去空白、去重、限长、颜色必须是 #rrggbb）。
	Tags []RecordTag `json:"tags"`
}

// ---------------- 下行：后端 -> 前端 ----------------

// 下行消息类型。
const (
	MsgWelcome       = "welcome"
	MsgError         = "error"
	MsgSymbols       = "symbols"
	MsgInstances     = "instances"
	MsgInstanceState = "instance_state"
	MsgEvent         = "event"
	MsgLeft          = "left"
	MsgDeleted       = "deleted"
	MsgPong          = "pong"
	MsgTemplates     = "templates"
	MsgStrategies    = "strategies"     // 可加载的策略清单
	MsgRecordings    = "recordings"     // 交易记录列表（摘要）
	MsgRecording     = "recording"      // 一条交易记录的完整内容（回放用）
	MsgTradeRecorded = "trade_recorded" // 一条交易记录刚刚收尾（列表要刷新 + 给一句提示）
)

// 广播的动作名（Event.Action）。
const (
	ActBarAdvanced      = "bar_advanced"
	ActAnnotationAdded  = "annotation_added"
	ActAnnotationMoved  = "annotation_moved"
	ActAnnotationStyled = "annotation_styled"
	ActAnnotationDelete = "annotation_deleted"
	ActAnnotationClear  = "annotation_cleared"
	ActOrderPlaced      = "order_placed"
	ActOrderFilled      = "order_filled"
	ActOrderMoved       = "order_moved" // 改挂单价
	ActOrderCancelled   = "order_cancelled"
	ActFlatAll          = "flat_all" // 市价退仓 + 撤销全部挂单
	ActControlChanged   = "control_changed"
	ActMembersChanged   = "members_changed"
	ActBotStarted       = "bot_started"
	ActBotStopped       = "bot_stopped"

	// ActRecordingAudio 是**交易记录里「语音步」的类型名**（v0.15.0 起录音自己占一步）。
	//
	// 名字里的 recording 是历史包袱：v0.18.0 起录制不再是一个用户可见的概念，
	// 但这一步的类型名留在文件格式里 —— 已经落盘的交易记录全靠它认语音步，
	// 改名等于让所有旧文件里的语音步变成「什么都没发生」的空档。
	ActRecordingAudio = "recording_audio"

	// 语音讲解（v0.16.0）。和上面那个分开：它们描述**盘面上多了一段/少了一段语音**，
	// 和有没有一条正在进行的交易记录无关。
	ActVoiceAdded   = "voice_added"
	ActVoiceCleared = "voice_cleared"
)

// StepBase 是第 1 步的类型：开始录制那一刻的盘面（没有事件，就是 Base 本身）。
// 其余步的 Kind 直接用动作名（bar_advanced / annotation_added / …），
// 录音步用 recording_audio —— 这样前端拿 Kind 就能判断类型，不必再拆 events。
const StepBase = "base"

// Point 是标注的一个端点：横坐标是「一根已揭示 K 线的时间 + 向右延伸的根数」，纵坐标是价格。
//
// Time 是**锚点**：必须是已揭示数据里某一根 K 线的时间。这条红线没变 —— 指向一根还没出现
// 的 K 线等于提前泄露后面的行情，而且那样的时间在数据里根本不存在。
//
// Offset 是「从锚点往右再走多少根」，只按 K 线根数延伸，不引用任何未来时间：
//
//	Offset = 0   端点就在锚点那根 K 线的中心（老存档没有这个字段，等价于 0）
//	Offset = 40  端点在锚点右边第 40 根，可以越过最后一根已揭示的 K 线
//
// 为什么是「根数」而不是「一个外推出来的未来时间字符串」：横轴本来就是按 K 线根数排的，
// 而未来的时间在一根 K 线都没出现时只能靠外推 —— 遇到周末/休市就错位，而且等新 K 线
// 揭示出来，同一个未来时间会落到另一个下标上，画好的线会自己左右跳。
// 锚点下标不变、偏移不变，屏幕位置就不会变。
type Point struct {
	Time  string  `json:"time"`
	Price float64 `json:"price"`
	// Offset 是向右延伸的 K 线根数，>= 0；为 0 时不出现在 JSON 里（旧存档/旧客户端照旧）。
	Offset int `json:"offset,omitempty"`
}

// Bar 是送给前端的一根 K 线。ema20 在 Go 里算好，前端只负责画 ——
// 指标口径只有一份，不会出现「终端和网页算出两条 EMA」。
//
// ATR **不在这里**（v0.23.0 起）：它的周期和平滑方式由用户在界面上现调
// （RMA / SMA / EMA / WMA），前端拿着 OHLC 自己算，见 web/src/components/chart/atr.ts。
//
// 价格在加载时已经四舍五入到两位小数（CSV 里有 89.97000000000001 这种浮点噪声，
// 直接下发会让价格轴和提示里冒出一串尾巴）。
type Bar struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
	// Ema20 在前 19 根还没有值，序列化成 null（前端据此断开折线）
	Ema20 *float64 `json:"ema20"`
}

// Annotation 是画在画布上的一个图形。五种图形都用「起点/终点」两个点表示：
// 趋势线/射线/箭头线是两点连成的一条线，矩形用两个对角点，斐波那契回撤用两点的
// 价格区间摊开四条水平线（见 FibLevel）。
type Annotation struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"` // trend | ray | rect | arrow | fib
	Start     Point           `json:"start"`
	End       Point           `json:"end"`
	Style     AnnotationStyle `json:"style"`
	Snapped   bool            `json:"snapped"` // 端点吸附过 OHLC（界面上画小圆点提示）
	CreatedBy string          `json:"createdBy"`
	CreatedAt int64           `json:"createdAt"`

	// LegacyColor 是 0.5.2 及以前唯一的颜色字段（那时候没有模板，整条线一个颜色）。
	// 只在读旧存档时有用：迁移时并进 Style.Color 后清空，所以新标注的 JSON 里没有它。
	LegacyColor string `json:"color,omitempty"`
}

// ---------------- 样式与模板 ----------------

// 线型。只有两种：实线、虚线（点线在 1px 上跟虚线看不出差别，不做）。
const (
	DashSolid  = "solid"
	DashDashed = "dashed"
)

// FibLevel 是斐波那契回撤的一条水平线。
//
// Ratio 是「相对起点到终点这段价格区间」的倍数：0 = 起点价格、1 = 终点价格、
// -1 = 起点再往下（或往上）一个区间、2 = 终点再往外一个区间。四条线永远是水平的、
// 互相平行 —— 用户只能调颜色，不能单独拖某一条（见 buildFigure 的注释）。
type FibLevel struct {
	Ratio float64 `json:"ratio"`
	Color string  `json:"color"`
}

// AnnotationStyle 是一个图形自己的样式。
//
// 它是**画下去那一刻从模板复制过来的快照**：之后再改模板、删模板，已经画好的图形不变。
// 这也是把它存进 Annotation 而不是只存 templateId 的原因 —— 否则模板一改，
// 昨天画在盘面上的所有线一起变色。
type AnnotationStyle struct {
	Color       string     `json:"color"`                 // 线条 / 矩形边框颜色
	Width       float64    `json:"width"`                 // 线宽（屏幕像素）
	Dash        string     `json:"dash"`                  // solid | dashed
	Fill        string     `json:"fill,omitempty"`        // 矩形填充色，空 = 只画边框
	FillOpacity float64    `json:"fillOpacity,omitempty"` // 矩形填充不透明度 0~1
	Levels      []FibLevel `json:"levels,omitempty"`      // 斐波那契的四个水平（只有 fib 用）
}

// Template 是一个工具下面的一套样式预设。模板是**全局的**（不属于某个实例）：
// 换个标的复盘，还是那几条常用的线。
type Template struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"` // trend | ray | rect | arrow | fib
	Name      string          `json:"name"`
	Style     AnnotationStyle `json:"style"`
	CreatedAt int64           `json:"createdAt"`
}

// 订单状态。
const (
	OrderOpen      = "open"
	OrderFilled    = "filled"
	OrderCancelled = "cancelled"
)

// 订单类型。
const (
	KindLimit  = "limit"
	KindStop   = "stop"
	KindMarket = "market" // 市价单：下单即按当前收盘价成交，不会留在挂单列表里
)

// Order 是一张挂单。没有止损/止盈类型：用户想止损就自己挂一张 stop 单。
//
// KindMarket 是唯一的例外：它只在「市价成交」那一刻存在，Status 一律是 filled，
// 记在 Orders 里是为了让成交、历史记录和撤单列表共用同一套数据结构。
type Order struct {
	ID          string  `json:"id"`
	Side        string  `json:"side"` // buy | sell
	Kind        string  `json:"kind"` // limit | stop | market
	Price       float64 `json:"price"`
	Quantity    float64 `json:"quantity"`
	Status      string  `json:"status"`      // open | filled | cancelled
	PlacedAt    string  `json:"placedAt"`    // 下单那一刻的 K 线时间
	PlacedIndex int     `json:"placedIndex"` // 下单那一刻的 K 线下标
	FilledAt    string  `json:"filledAt,omitempty"`
	FilledIndex int     `json:"filledIndex"` // -1 = 没成交
	FillPrice   float64 `json:"fillPrice,omitempty"`

	// Group 是 OCO 组：同一组最多成交一张，其余在本根自动撤销。
	// 空 = 独立挂单（用户手动下的单都是这种）。
	// 机器人开仓时给止损和止盈打上同一组 —— 5 分钟 K 线看不出高低点谁先出现，
	// 不打组的话同根双触发会把仓位平两遍，凭空多出一个反向仓。
	Group string `json:"group,omitempty"`
}

// Trade 是一笔成交（= 一次挂单被触发）。不做手续费/滑点；成交价通常等于挂单价，
// 只有开盘就跳过挂单价（跳空）时取开盘价 —— 见 instance.go 的 fillPriceFor。
type Trade struct {
	ID          string  `json:"id"`
	OrderID     string  `json:"orderId"`
	Time        string  `json:"time"`
	Side        string  `json:"side"`
	Kind        string  `json:"kind"`
	Price       float64 `json:"price"`
	Quantity    float64 `json:"quantity"`
	RealizedPnl float64 `json:"realizedPnl"` // 这一笔平掉的部分实现的盈亏；开仓/加仓为 0
}

// ClosedLot 是**一批已经平掉的仓位**：从哪一批开仓成交来、在哪一次平仓成交结束、
// 平掉多少手，以及这一批自己实现盈亏多少。
//
// 它和 Trade 的分工：Trade 是「一笔成交」（可能开仓/加仓/平仓/反手都算），
// ClosedLot 是「一批仓位的完整生命周期」。一笔平仓成交可以同时平掉好几批，
// 那就产出好几条 ClosedLot —— 画布上每根平仓虚线对应其中一条。
//
// 盈亏用**这一批自己的开仓价**算，不用平均成本：画布上那条线的两个端点就是
// 「这一批的开仓价」和「平仓价」，信息卡里也照这两个数显示已实现盈亏，
// 三者必须是同一个算法算出来的。用均价会把 5 笔不同价位的空单显示成一模一样的数。
//
// ⚠️ 一笔平仓成交把**全部**持仓平掉时，各批盈亏之和与该笔 Trade.RealizedPnl
// （平均成本法）完全相等；只平掉一部分时两者会不同 —— 那是两种成本法的固有差异，
// 不是算错。仓位与账户口径一律以 applyFill 的平均成本法为准，这里只负责「逐批归因」。
type ClosedLot struct {
	OpenTime    string  `json:"openTime"`  // 开仓成交的 K 线时间（= 那个三角的横坐标）
	OpenPrice   float64 `json:"openPrice"` // 开仓的真实成交价（三角的纵坐标）
	CloseTime   string  `json:"closeTime"`
	ClosePrice  float64 `json:"closePrice"`
	Side        string  `json:"side"`        // long = 买开的仓，short = 卖开的仓
	Quantity    float64 `json:"quantity"`    // 这一批被平掉的手数
	RealizedPnl float64 `json:"realizedPnl"` // 这一批自己的已实现盈亏（美元）
}

// Position 是净头寸（带符号的数量 + 平均成本）。
type Position struct {
	Quantity     float64 `json:"quantity"` // 正=多 负=空 0=空仓
	Side         string  `json:"side"`     // long | short | flat
	AveragePrice float64 `json:"averagePrice"`
}

// Member 是实例里的一个人。
type Member struct {
	ClientID  string `json:"clientId"`
	Name      string `json:"name"`
	Operator  bool   `json:"operator"`
	Connected bool   `json:"connected"`
}

// Event 是一条操作记录。所有会改状态的操作都会广播一条，广播内容与落库内容一致，
// 所以「谁在什么时候做了什么」在后端是完整的（便于演示回看与排查）。
type Event struct {
	Seq     int    `json:"seq"`
	At      int64  `json:"at"`
	By      string `json:"by"`
	Action  string `json:"action"`
	Payload any    `json:"payload,omitempty"`
}

// InstanceSummary 是实例列表里的一行。
type InstanceSummary struct {
	ID         string `json:"id"`
	Symbol     string `json:"symbol"`
	Anchor     string `json:"anchor"`
	Cursor     int    `json:"cursor"`
	LoadedBars int    `json:"loadedBars"`
	Members    int    `json:"members"`
	Operator   string `json:"operator"`
	Bot        bool   `json:"bot"`
	// BotStrategy 是机器人正在跑的策略 id；空 = 随机演示机器人。
	BotStrategy string `json:"botStrategy,omitempty"`
	CreatedBy   string `json:"createdBy"`
	CreatedAt   int64  `json:"createdAt"`
	// 实时盈亏摘要：列表上就能看到这个实例做到哪儿了
	RealizedPnl float64 `json:"realizedPnl"`
	Trades      int     `json:"trades"`
}

// InstanceDTO 是进入实例时下发的一次性全量快照。
//
// **只包含已经揭示的 K 线**（Bars 长度 = Cursor+1）：后面的行情还在服务端，
// 前端拿不到就不会「不小心」预览到未来。
type InstanceDTO struct {
	ID            string       `json:"id"`
	Symbol        string       `json:"symbol"`
	Folder        string       `json:"folder"`
	Anchor        string       `json:"anchor"`
	Cursor        int          `json:"cursor"`
	Tick          float64      `json:"tick"`      // 最小跳动，价格轴按它定小数位
	TickValue     float64      `json:"tickValue"` // 1 tick 的美元价值（0 = 没配置，按每点 1 美元）
	Bars          []Bar        `json:"bars"`
	HiddenBars    int          `json:"hiddenBars"` // 已加载但还没揭示的根数
	HasMore       bool         `json:"hasMore"`    // 数据目录里还有没有更晚的日文件
	Annotations   []Annotation `json:"annotations"`
	Orders        []Order      `json:"orders"`
	Trades        []Trade      `json:"trades"`
	ClosedLots    []ClosedLot  `json:"closedLots"` // 逐批平仓记录（画布上那些虚线的数据源）
	Position      Position     `json:"position"`
	RealizedPnl   float64      `json:"realizedPnl"`
	UnrealizedPnl float64      `json:"unrealizedPnl"`
	Operator      string       `json:"operator"`
	Members       []Member     `json:"members"`
	Bot           bool         `json:"bot"`
	// BotStrategy / BotQuantity 让「刷新页面之后」的界面还能说清机器人在跑什么。
	// 只存在内存里：机器人是进程内的运行状态，不跟存档走（见 persist.go）。
	BotStrategy string   `json:"botStrategy,omitempty"`
	BotQuantity float64  `json:"botQuantity,omitempty"`
	Seq         int      `json:"seq"`
	Events      []Event  `json:"events"`
	Notes       []string `json:"notes"`
	CreatedBy   string   `json:"createdBy"`
	CreatedAt   int64    `json:"createdAt"`
	// Voice 是当前这根 K 线上的语音讲解（v0.16.0）。
	//
	// **永远是对象，不是 null**：语音和绘图一样是盘面的一部分，没录过就是空清单。
	// 它不依赖任何「录制」状态 —— 那个概念在 v0.18.0 之后只存在于后端内部。
	Voice VoiceState `json:"voice"`
	// VoiceAll 是整个实例的语音清单（按 K 线时间排序）。
	//
	// 为什么清单要单独发一份：VoiceState 只装**当前这根**（否则每根 K 线都背着
	// 一份越来越长的数组），但复盘只能往前走、不能倒回 —— 离开一根之后就再也
	// 回不去了。没有这份全量清单，早先几根上讲的语音会变成「听得见总数、点不到」。
	// 它只在快照和 voice_added / voice_cleared 时下发，bar_advanced 不带。
	VoiceAll []AudioClip `json:"voiceAll"`
}

// You 告诉这条连接「你是谁」，前端据此决定按钮能不能点。
type You struct {
	ClientID string `json:"clientId"`
	Name     string `json:"name"`
	Operator bool   `json:"operator"`
}

// ---------------- 各类下行消息 ----------------

type WelcomeMsg struct {
	Type     string `json:"type"`
	ClientID string `json:"clientId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

type ErrorMsg struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Request  string `json:"request,omitempty"`
	Instance string `json:"instanceId,omitempty"`
}

type SymbolsMsg struct {
	Type    string       `json:"type"`
	Symbols []SymbolInfo `json:"symbols"`
}

type InstancesMsg struct {
	Type      string            `json:"type"`
	Instances []InstanceSummary `json:"instances"`
}

type InstanceStateMsg struct {
	Type     string      `json:"type"`
	You      You         `json:"you"`
	Instance InstanceDTO `json:"instance"`
}

type EventMsg struct {
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
	Seq        int    `json:"seq"`
	At         int64  `json:"at"`
	By         string `json:"by"`
	Action     string `json:"action"`
	Payload    any    `json:"payload,omitempty"`
}

type LeftMsg struct {
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
}

type DeletedMsg struct {
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
}

type PongMsg struct {
	Type string `json:"type"`
}

// TemplatesMsg 是模板全量列表。模板很少（每个工具几条），所以任何一次改动都
// 直接把整份列表广播给所有人，前端不用维护增量合并。
//
// LastSavedID 是「这次刚存下来的是哪一条」：新建模板时 id 由后端分配，
// 前端靠它把刚建好的那条立刻选上（不然只能按名字猜）。
type TemplatesMsg struct {
	Type        string     `json:"type"`
	Templates   []Template `json:"templates"`
	LastSavedID string     `json:"lastSavedId,omitempty"`
}

// SymbolInfo 是选标的时的一项（来自数据目录，不读文件内容）。
type SymbolInfo struct {
	Symbol    string `json:"symbol"`
	Folder    string `json:"folder"`
	Days      int    `json:"days"`
	FirstDate string `json:"firstDate"`
	LastDate  string `json:"lastDate"`
	// Tick 是最小跳动（0.25 / 0.1 / 0.01…），来自 _symbols.json，缺省按标的名兜底。
	// 前端用它决定价格轴与提示的小数位。
	Tick float64 `json:"tick"`
	// TickValue 是一个 tick 值多少美元（MNQ 0.5、MES 1.25、MGC 1、CL 10），
	// 0 = 没配置。复盘结算盈亏用它，界面只做展示。
	TickValue float64 `json:"tickValue"`
}

// ---------------- 广播负载 ----------------

// BarAdvancedPayload 是推进一根 K 线之后的结果。
// 订单/成交/持仓每次都发全量：它们数量很少（一次复盘通常个位数），
// 全量发送让前端不用维护「增量合并」这套容易出错的逻辑。
type BarAdvancedPayload struct {
	Bar           Bar         `json:"bar"`
	Cursor        int         `json:"cursor"`
	HiddenBars    int         `json:"hiddenBars"`
	HasMore       bool        `json:"hasMore"`
	Orders        []Order     `json:"orders"`
	Trades        []Trade     `json:"trades"`
	ClosedLots    []ClosedLot `json:"closedLots"`
	Position      Position    `json:"position"`
	RealizedPnl   float64     `json:"realizedPnl"`
	UnrealizedPnl float64     `json:"unrealizedPnl"`
	Fills         []Trade     `json:"fills"` // 这一根 K 线触发成交的单（可能为空）
	// Voice 是换根之后的语音状态（v0.16.0）：清单一换，界面上的「本根 x/9」立刻跟着走。
	// 和 Recording 不同 —— 它和有没在录制无关，所以放在这条「每根都发」的广播里。
	Voice VoiceState `json:"voice"`
}

type AnnotationPayload struct {
	Annotation Annotation `json:"annotation"`
}

type AnnotationDeletedPayload struct {
	AnnotationID string `json:"annotationId"`
}

// AnnotationClearedPayload 是「清空全部绘图」的广播负载。
//
// 为什么不是连发 N 条 annotation_deleted：那条路会在操作记录里留下 N 行噪音，
// 而且中途断线会留下「删了一半」的状态。一次清空 = 一条广播 = 一个原子状态。
//
// 带上被删掉的编号，是为了让操作记录能写清「清掉的是哪几个」——只给个数量的话，
// 事后回看只能知道「有人清了 5 个」，不知道清的是哪 5 个。
type AnnotationClearedPayload struct {
	Count         int      `json:"count"`
	AnnotationIDs []string `json:"annotationIds"`
}

type OrdersPayload struct {
	Orders []Order `json:"orders"`
}

// OrderMovedPayload 是改挂单价的广播负载。
//
// 比 OrdersPayload 多带一个 Order：操作记录里要写清「改的是哪一张、改成了多少」，
// 只给全量列表的话，前端只能靠猜（列表里没有「刚改的那张」的标记）。
type OrderMovedPayload struct {
	Order  Order   `json:"order"`
	Orders []Order `json:"orders"`
}

// OrderFilledPayload 是市价单成交的广播负载。
//
// 和挂单不同，市价单会**立刻**改持仓、成交和盈亏，所以一次把订单、成交、持仓、盈亏
// 全部发全量：前端不用再要一次快照，也不会出现「成交了但持仓没变」的中间态。
type OrderFilledPayload struct {
	Order         Order       `json:"order"`
	Trade         Trade       `json:"trade"`
	Orders        []Order     `json:"orders"`
	Trades        []Trade     `json:"trades"`
	ClosedLots    []ClosedLot `json:"closedLots"`
	Position      Position    `json:"position"`
	RealizedPnl   float64     `json:"realizedPnl"`
	UnrealizedPnl float64     `json:"unrealizedPnl"`
}

// FlatAllPayload 是【市价退仓并取消所有挂单】的广播负载（v0.20.0）。
//
// 一次动作可能做两件事：撤掉全部挂单、按现价把整个仓位平掉。所以它既不是
// OrdersPayload（还带着成交和持仓）也不是 OrderFilledPayload（本来空仓时没有成交）：
// `Order` / `Trade` 是**指针**，只有真的有仓位时才有值。
//
// 空仓 + 没挂单是唯一会被拒的情况（返回错误，不广播）—— 静默成功会让用户
// 以为「点了一下什么都没发生」，而到底是「没东西可平」还是「按钮坏了」分不清。
type FlatAllPayload struct {
	Order         *Order      `json:"order,omitempty"`
	Trade         *Trade      `json:"trade,omitempty"`
	Cancelled     int         `json:"cancelled"` // 撤销了几张挂单
	Orders        []Order     `json:"orders"`
	Trades        []Trade     `json:"trades"`
	ClosedLots    []ClosedLot `json:"closedLots"`
	Position      Position    `json:"position"`
	RealizedPnl   float64     `json:"realizedPnl"`
	UnrealizedPnl float64     `json:"unrealizedPnl"`
}

type ControlChangedPayload struct {
	Operator string `json:"operator"`
}

type MembersChangedPayload struct {
	Members []Member `json:"members"`
}

// BotPayload 是机器人启动的广播负载。
//
// Strategy 为空表示老式的随机演示机器人；非空时是策略 id，Name 是给人看的名字。
// 前端靠这两个字段把「启动机器人」按钮改成「停止 Caveman」。
type BotPayload struct {
	IntervalMs int     `json:"intervalMs"`
	Strategy   string  `json:"strategy,omitempty"`
	Name       string  `json:"name,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
}

// StrategyParam 是一个策略参数的描述（前端启动面板按它列出旋钮）。
type StrategyParam struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
	Help    string  `json:"help"`
}

// StrategyInfo 是「有一个什么策略可以跑」。
//
// 只发描述，不发实现：前端不需要知道策略内部怎么判断，
// 它只负责把 id 回传过来启动，剩下的全在后端。
type StrategyInfo struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Summary string          `json:"summary"`
	Params  []StrategyParam `json:"params"`
}

// StrategiesMsg 是策略清单。策略是编译期注册的，运行期不会变，
// 但每次请求都发全量 —— 前端连上就要一次，没有必要做缓存失效。
type StrategiesMsg struct {
	Type       string         `json:"type"`
	Strategies []StrategyInfo `json:"strategies"`
}

// ---------------- 交易记录（v0.11.0 起叫「录像」，v0.18.0 起是自动的交易记录） ----------------

// TradeStats 是**一笔交易**的统计，由录制到的操作算出来。
//
// 它解决的问题：交易记录里存的是「每一步做了什么」，而人想看的是一行结论 ——
// 这笔是多是空、几手、进出在哪个价、赚了多少、途中最多浮亏多少。
//
// 口径（全部由后端算，前端只排版）：
//
//	EntryPrice/EntryTime  这一笔**第一次成交**的价格与 K 线时间（开仓）
//	ExitPrice/ExitTime    最后一次成交的价格与 K 线时间（部分平仓时会一路更新）
//	Quantity/MaxQuantity  开仓手数 / 持仓期间的最大手数（加减仓时两者不同）
//	RealizedPnl           这笔交易全部成交的已实现盈亏之和（美元）
//	Mfe/Mae               最大有利/不利波动（美元，Mfe >= 0 >= Mae）
//	BarsHeld              持仓期间完整走过的 K 线根数
//
// Mfe/Mae 只统计「开仓之后、平仓之前**没有成交**的整根 K 线」：一根 K 线里
// 只有 OHLC，成交发生在这一根的开盘、盘中还是收盘根本看不出来。把开仓那根、
// 平仓那根的高低点也算进来，等于凭空替交易者选了一个最有利/最不利的时刻 ——
// 那种数字看着更"完整"，但经不起追问。同一根 K 线内开平（日内往返）时两项都是 0。
type TradeStats struct {
	Side        string  `json:"side"` // long / short（开仓方向）
	Quantity    float64 `json:"quantity"`
	MaxQuantity float64 `json:"maxQuantity"`
	EntryTime   string  `json:"entryTime"`
	EntryPrice  float64 `json:"entryPrice"`
	ExitTime    string  `json:"exitTime"`
	ExitPrice   float64 `json:"exitPrice"`
	RealizedPnl float64 `json:"realizedPnl"`
	Mfe         float64 `json:"mfe"`
	Mae         float64 `json:"mae"`
	BarsHeld    int     `json:"barsHeld"`
	Fills       int     `json:"fills"` // 成交笔数（开仓 + 加减仓 + 平仓）
	Steps       int     `json:"steps"` // 这段记录里的操作步数（含起点）
	// Closed 表示这一笔是否**正常平完**。false = 记录结束时仓位还在
	// （实例被删、程序退出、或者进程崩溃留下的草稿）—— 那不是数据丢了，
	// 是这笔交易当时确实还没结束。
	Closed bool `json:"closed"`
}

// RecordTag 是用户给一笔交易打的**自定义标签**（v0.21.0）。
//
// 名字和颜色都由用户定，服务端不当老师：只要求名字非空、不超过 maxTagNameRunes，
// 颜色是 #rrggbb。为什么颜色跟着标签走而不是「标签名 → 主题色」查表：
// 同一个词在不同人眼里轻重不同（「追单」有人标红、有人标黄），颜色是用户自己的记号，
// 存在标签上才能做到「打过的标签下次点一下就还原成一模一样的样子」。
type RecordTag struct {
	Name  string `json:"name"`
	Color string `json:"color"` // #rrggbb
}

// 交易记录的**操作来源**（v0.21.0）。
//
// 为什么单独存而不是从 CreatedBy 猜：CreatedBy 是「谁开的这一笔」，而来源问的是
// 「这些操作是谁做的」。机器人接管时 by 是 "bot"，可真人开的实例被机器人接管后
// 开出来的那一笔也该算机器人 —— 所以来源在**开仓那一刻**就定下来存进记录，
// 后面的筛选才可能和回放里看到的一致。
const (
	SourceHuman = "human" // 真人手动
	SourceBot   = "bot"   // 机器人

	// SourceDemoBot 是「随机演示机器人」的来源 id。它不是一个注册策略
	// （见 bot.go：不填 strategy 就是那个只会乱点乱下单的演示机器人），
	// 所以这里给它一个固定的 id，前端筛选按它分组。
	SourceDemoBot  = "demo"
	SourceDemoName = "随机演示"
)

// RecordingSummary 是交易记录列表里的一行。
type RecordingSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Symbol  string `json:"symbol"`
	Folder  string `json:"folder"`
	Anchor  string `json:"anchor"`  // 第 1 步（开仓前那一屏）的 K 线时间
	EndTime string `json:"endTime"` // 最后一步那根 K 线的时间
	// InstanceID 是这笔交易发生在哪个实例上。**只用于追溯和显示**：
	// 交易记录是独立的一份数据，实例被删掉之后这里会指向一个不存在的 id，
	// 打开、回放、删除都不依赖它（见 recording.go 的文件头）。
	InstanceID string `json:"instanceId,omitempty"`
	// StepCount / AudioCount / EventCount / Bars 是**摘要里的计数**，名字带 Count 是为了
	// 不和 Recording 里的 Steps（每一步的事件数组）撞 JSON 名 —— 一旦撞了，
	// Go 的内嵌字段会被外层同名字段盖掉，列表里的「12 步」会变成一段 JSON 数组。
	//
	// 一步 = 一次操作，所以 StepCount 会比「根数」大很多：一根 K 线上画 3 个图形 = 3 步。
	StepCount  int        `json:"stepCount"`  // 共几步（含第 1 步；语音步也算）
	AudioCount int        `json:"audioCount"` // 共几条语音
	EventCount int        `json:"eventCount"` // 共多少个改盘面的操作（不含语音步）
	Bars       int        `json:"bars"`       // 推进了几根 K 线（含起点那根）
	Size       int64      `json:"size"`       // 目录占用（JSON + 音频）
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  int64      `json:"createdAt"`
	EndedAt    int64      `json:"endedAt"` // 收尾时间；持仓中还开着时等于最后一次写盘时间
	Stats      TradeStats `json:"stats"`

	// Source / SourceID / SourceName 是**操作来源**（v0.21.0，见上面的常量）。
	//
	// SourceName 是给人看的细分：真人 = 操作者名字；机器人 = 策略名
	// （「随机演示」/「Caveman（野人）」）。筛选按 Source + SourceID 分组，
	// 名字只负责显示 —— 策略改名不该让历史记录的筛选分组跟着裂开。
	//
	// 旧记录（v0.20.0 及以前）没有这三个字段，读取时按 CreatedBy 反推
	// （见 RecordingSummary.normalizeSource），所以前端永远拿得到口径一致的来源。
	Source     string `json:"source,omitempty"`
	SourceID   string `json:"sourceId,omitempty"`
	SourceName string `json:"sourceName,omitempty"`

	// Tags 是用户打的自定义标签（v0.21.0）。顺序即打标签的顺序，界面上按这个顺序排。
	Tags []RecordTag `json:"tags,omitempty"`
}

// Recording 是一条交易记录的全部内容。
//
// 它**不是视频**，而是「开仓前那一屏 + 之后每一个操作的原始广播」：
//
//	Base   第 1 步那一刻的完整画布（还没有这笔仓位的盘面）
//	Steps  第 2 步起**每个操作一段**，开仓那一笔成交就是第 2 步
//
// 回放就是把 Base 深拷贝一份，然后按顺序把事件重新「演」一遍 —— 和实时界面
// 用的是同一套状态更新逻辑（前端 stores/instance-reducer.ts），所以
// **回放看到的画面和当时看到的必然一模一样**，包括机器人做的操作。
type Recording struct {
	RecordingSummary
	// Format 是记录格式版本：2 = 一步一个操作（v0.15.0 起），1 = 旧的一步一根 K 线。
	// 两种格式的 Steps 语义不同，所以**不迁移、不混读**：scan 跳过 1，Load 明确报错。
	Format int `json:"format"`

	Tick      float64 `json:"tick"`
	TickValue float64 `json:"tickValue"`

	Base   RecordingBase   `json:"base"`
	Steps  []RecordingStep `json:"steps"`
	Audios []AudioClip     `json:"audios"`
}

// RecordingBase 是开仓**之前**那一刻的盘面。
//
// 关键在「之前」：第 2 步就是开仓成交，回放从空仓开始演 —— 这样「怎么进的场」
// 才有可能看清楚。如果 Base 取成开仓之后，回放的第一帧就已经带着仓位，
// 入场那一下永远不会出现。
type RecordingBase struct {
	Cursor        int          `json:"cursor"`
	Bars          []Bar        `json:"bars"`
	Annotations   []Annotation `json:"annotations"`
	Orders        []Order      `json:"orders"`
	Trades        []Trade      `json:"trades"`
	ClosedLots    []ClosedLot  `json:"closedLots"`
	Position      Position     `json:"position"`
	RealizedPnl   float64      `json:"realizedPnl"`
	UnrealizedPnl float64      `json:"unrealizedPnl"`
}

// RecordingStep 是交易记录里的一步 —— **一步 = 一次真实操作**（v0.15.0 起）。
//
// 第 1 步是起点：Kind = StepBase，Events 为空（它就是 Base 本身）。
// 之后每一条放行的广播开一步，所以「第 N 步」和「用户第 N 次动手」永远对得上：
// 点一次【下一根】、画一个图形、拖一次挂单价、录一段音，各占一步。
//
// 语音步（Kind = recording_audio）特殊：Events 为空（它不改盘面），
// clip 放在 Audio 里。回放到这一步画面不动，但语音该响。
type RecordingStep struct {
	Step int `json:"step"`
	// At 是这一步的真实发生时刻（Unix 毫秒）。秒不够用：同一秒里连着画两个图形很常见，
	// 而复盘时「这一步和上一步隔了 3 秒还是 30 秒」恰恰是判断操作节奏的线索。
	At     int64      `json:"at"`
	Kind   string     `json:"kind"`            // StepBase / 动作名 / recording_audio
	By     string     `json:"by,omitempty"`    // 谁动的（human / bot / system）
	Time   string     `json:"time"`            // 这一步发生时**当前那根 K 线**的时间
	Cursor int        `json:"cursor"`          // 这一步发生后的游标（跳步时自检用）
	Audio  *AudioClip `json:"audio,omitempty"` // 仅录音步：挂在这一步上的那一段
	// Events 通常只有 1 条（一条放行的广播 = 一步）。留着数组是因为协议层不该
	// 假设「一步永远只有一条」—— 将来真要把「同时发生的两条」并成一步，结构不用动。
	// 录音步为空数组（不是 null，见 recording.go 里的注释）。
	Events []RecordingEvent `json:"events"`
}

// RecordingEvent 是一条被录下来的操作，字段与广播一一对应。
//
// Payload 保持原始 JSON：回放端直接把它交给状态更新逻辑，不重新构造 ——
// 任何「翻译一层」的做法都会让回放和实时出现细微差异，而这正是要避免的。
type RecordingEvent struct {
	Seq     int             `json:"seq"`
	At      int64           `json:"at"`
	By      string          `json:"by"` // human / human2 / bot
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// AudioClip 是一段挂在某根 K 线上的录音。
//
// 存储键是 **K 线时间**（Time），不是下标、不是时间戳：回放时按时间找挂载点，
// 录像内数据被重排也不会错位。Step 是冗余的一份，指的是**录音那一步**的步号
// （v0.15.0 起录音自己占一步），列表里显示「第几步」用。
//
// File 是录像目录里的文件名（相对路径），音频本身不进 JSON。
type AudioClip struct {
	ID         string `json:"id"`
	Step       int    `json:"step"`
	Time       string `json:"time"`
	DurationMs int    `json:"durationMs"`
	File       string `json:"file"`
	Size       int64  `json:"size"`
	CreatedAt  int64  `json:"createdAt"`
	// By 是「谁录的」（human / bot / system）。v0.16.0 起录音成了共享盘面的一部分，
	// 多人时列表里要能看出这条是谁讲的；旧录像里没有这个字段，是空串。
	By string `json:"by,omitempty"`
}

// TradeRecordedMsg 是「一笔交易刚刚收尾、记录已保存」的通知。
//
// 它不是一条广播（不进实例的操作记录），而是一条**一次性消息**：交易记录属于
// 独立的数据集，和实例的事件流不是一回事。前端拿它做两件事 —— 刷新列表、
// 给一句「这笔赚了多少」的轻提示，不需要因此把界面切到「正在记录」的状态
// （那个状态从头到尾都不该被用户看见）。
//
// Warning 非空表示记录本身是完整的，只是没落到磁盘（没配目录 / 磁盘满），
// 说清楚即可，不能因此把已经录完的内容一起丢掉。
type TradeRecordedMsg struct {
	Type      string           `json:"type"`
	Recording RecordingSummary `json:"recording"`
	Dir       string           `json:"dir,omitempty"`
	Warning   string           `json:"warning,omitempty"`
}

// ---------------- 语音讲解（v0.16.0） ----------------

// VoiceAddedPayload 是「盘面上多了一段语音」的广播。
//
// 只有盘面这一份：语音是盘面数据，和有没有一条正在进行的交易记录无关 ——
// 交易记录里那一份是后端顺手抄进去的（见 voice.go 的 addVoiceLocked）。
type VoiceAddedPayload struct {
	Audio AudioClip `json:"audio"`
	// Voice 是这根 K 线**加完之后**的清单与计数（前端照它显示，不自己 +1）。
	Voice VoiceState `json:"voice"`
	// VoiceAll 是整个实例的清单（见 InstanceDTO.VoiceAll）：多一根 K 线上的语音，
	// 界面上那个「其他 N 条」也得跟着变。
	VoiceAll []AudioClip `json:"voiceAll"`
}

// VoiceClearedPayload 是「这根 K 线上的语音全被清掉」的广播。
//
// Removed 带着被删掉的那几条（文件名要一起带，前端可能正在播其中一条，得停下）。
// 交易记录里对应的语音步会被一起去掉（步号重排），那是后端内部的事。
type VoiceClearedPayload struct {
	Removed  []AudioClip `json:"removed"`
	Voice    VoiceState  `json:"voice"`
	VoiceAll []AudioClip `json:"voiceAll"`
}

// RecordingsMsg 是交易记录列表。
type RecordingsMsg struct {
	Type       string             `json:"type"`
	Dir        string             `json:"dir"`
	Recordings []RecordingSummary `json:"recordings"`
}

// RecordingMsg 是一条交易记录的完整内容。
type RecordingMsg struct {
	Type      string     `json:"type"`
	Recording *Recording `json:"recording"`
}
