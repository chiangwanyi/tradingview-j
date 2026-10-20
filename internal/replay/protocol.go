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
	ReqHello         = "hello"
	ReqListSymbols   = "list_symbols"
	ReqListInstances = "list_instances"
	ReqCreate        = "create_instance"
	ReqJoin          = "join_instance"
	ReqLeave         = "leave_instance"
	ReqDelete        = "delete_instance"
	ReqTakeControl   = "take_control"
	ReqRelease       = "release_control"
	ReqStep          = "step"
	ReqAnnAdd        = "annotation_add"
	ReqAnnMove       = "annotation_move"
	ReqAnnDelete     = "annotation_delete"
	ReqAnnClear      = "annotation_clear" // 一次清掉当前实例的全部绘图（画布右键菜单「移除 N 个绘图」）
	ReqListTemplates = "list_templates"
	ReqSaveTemplate  = "save_template"
	ReqDelTemplate   = "delete_template"
	ReqOrderPlace    = "order_place"
	ReqOrderMarket   = "order_market"
	ReqOrderModify   = "order_modify" // 改挂单价：画布上拖动挂单线走这条
	ReqOrderCancel   = "order_cancel"
	ReqBotStart      = "bot_start"
	ReqBotStop       = "bot_stop"
	ReqPing          = "ping"

	// 录制与回放（v0.11.0 起）
	ReqRecordStart       = "record_start"        // 开始录制：把当前盘面记为第 1 步
	ReqRecordStop        = "record_stop"         // 结束录制并落盘
	ReqRecordAudio       = "record_audio"        // 一条录音，挂在当前那根 K 线上
	ReqRecordAudioDelete = "record_audio_delete" // 清空**当前这一步**的录音
	ReqListRecordings    = "list_recordings"     // 录像列表
	ReqGetRecording      = "get_recording"       // 取一条录像的全部内容（回放用）
	ReqDeleteRecording   = "delete_recording"
)

// Request 是一条上行消息。每种类型只读自己那几个字段。
type Request struct {
	Type string `json:"type"`

	// hello
	Name string `json:"name"`

	// create_instance
	Symbol      string `json:"symbol"`
	Time        string `json:"time"`        // 空 = 由后端随机挑一个时间点
	Bars        int    `json:"bars"`        // 画布上初始显示多少根（含锚点之前的历史），0 = 默认
	ForwardDays int    `json:"forwardDays"` // 锚点之后预加载几天，0 = 默认
	Seed        int64  `json:"seed"`        // 非 0 时随机锚点可复现（测试/复现问题用）

	// 所有和某个实例有关的请求
	InstanceID string `json:"instanceId"`

	// annotation_add / annotation_move
	Kind  string           `json:"kind"` // trend | ray | rect | arrow | fib
	Start *Point           `json:"start"`
	End   *Point           `json:"end"`
	Style *AnnotationStyle `json:"style"` // 只在 annotation_add 里用；画下去那一刻的快照
	Snap  bool             `json:"snap"`  // true = 端点吸附到该 K 线的 OHLC（前端按住 Ctrl）

	// annotation_move / annotation_delete
	AnnotationID string `json:"annotationId"`

	// save_template
	Template *Template `json:"template"`
	// delete_template
	TemplateID string `json:"templateId"`

	// order_place / order_market
	Side     string  `json:"side"` // buy | sell
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`

	// order_cancel / order_modify
	OrderID string `json:"orderId"`

	// bot_start
	IntervalMs int `json:"intervalMs"`

	// record_audio：一段 WAV 的 base64。**不走文件上传** —— 录音只有几十 KB
	// 到几百 KB，和别的动作一样走这条有序的消息流，顺序天然和操作对得上。
	AudioData  string `json:"audioData"`
	DurationMs int    `json:"durationMs"`

	// get_recording / delete_recording
	RecordingID string `json:"recordingId"`
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
	MsgRecordings    = "recordings" // 录像列表（摘要）
	MsgRecording     = "recording"  // 一条录像的完整内容（回放用）
)

// 广播的动作名（Event.Action）。
const (
	ActBarAdvanced      = "bar_advanced"
	ActAnnotationAdded  = "annotation_added"
	ActAnnotationMoved  = "annotation_moved"
	ActAnnotationDelete = "annotation_deleted"
	ActAnnotationClear  = "annotation_cleared"
	ActOrderPlaced      = "order_placed"
	ActOrderFilled      = "order_filled"
	ActOrderMoved       = "order_moved" // 改挂单价
	ActOrderCancelled   = "order_cancelled"
	ActControlChanged   = "control_changed"
	ActMembersChanged   = "members_changed"
	ActBotStarted       = "bot_started"
	ActBotStopped       = "bot_stopped"

	// 录制（v0.11.0）。这四个动作**不会被录进录像本身** —— 否则回放时会
	// 出现「第 3 步里还有一条开始录制」，那是元信息，不是盘面操作。
	ActRecordingStarted      = "recording_started"
	ActRecordingStopped      = "recording_stopped"
	ActRecordingAudio        = "recording_audio"
	ActRecordingAudioCleared = "recording_audio_cleared"
)

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
	CreatedBy  string `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
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
	Seq           int          `json:"seq"`
	Events        []Event      `json:"events"`
	Notes         []string     `json:"notes"`
	CreatedBy     string       `json:"createdBy"`
	CreatedAt     int64        `json:"createdAt"`
	// Recording 是这个实例当前的录制状态（没在录时是 null）。
	// 只有操作者能开始录制，但**所有人都能看到这个标记** —— 演示时观众要知道
	// 屏幕上正在被录，否则「刚才那段怎么没录进去」根本无从判断。
	Recording *RecordingState `json:"recording,omitempty"`
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

type ControlChangedPayload struct {
	Operator string `json:"operator"`
}

type MembersChangedPayload struct {
	Members []Member `json:"members"`
}

type BotPayload struct {
	IntervalMs int `json:"intervalMs"`
}

// ---------------- 录制与回放（v0.11.0） ----------------

// RecordingState 是「这个实例现在正在录吗」的实时状态。
//
// 它跟着实例快照一起下发，也随 recording_started / recording_stopped /
// recording_audio / recording_audio_cleared 四条广播更新。**步数是服务端数的**，
// 前端只负责显示 —— 前端数的步数和「录像文件里到底有几段」必须永远是同一个数。
//
// 录音的计数分两层，别混：
//
//	StepAudios/MaxAudios  当前这一步（这根 K 线）几条、上限几条 —— HUD 显示的就是这个
//	Audios                整条录像一共几条 —— **不限**，只有单步才限 9 条
//
// Clips 是**当前这一步**的录音清单：HUD 要列出来给用户预览、逐条删。
// 带在 state 里而不是让前端自己攒，是因为「切步」时前端不该自己去猜哪几条属于新的一步。
type RecordingState struct {
	Active    bool   `json:"active"`
	ID        string `json:"id,omitempty"`
	By        string `json:"by,omitempty"`        // 谁在录
	StartedAt int64  `json:"startedAt,omitempty"` // 开始录制的时间（Unix 秒）
	Step      int    `json:"step"`                // 当前第几步（1 = 点录制时那一屏）
	StartTime string `json:"startTime,omitempty"` // 第 1 步对应的 K 线时间
	Current   string `json:"current,omitempty"`   // 当前这一步对应的 K 线时间
	Audios    int    `json:"audios"`              // 整条录像已录几条（不限）
	StepAudios int   `json:"stepAudios"`          // 当前这一步已录几条
	MaxAudios int    `json:"maxAudios"`           // 单步上限（9）
	Clips     []AudioClip `json:"clips"`          // 当前这一步的录音
}

// RecordingSummary 是录像列表里的一行。
type RecordingSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Symbol  string `json:"symbol"`
	Folder  string `json:"folder"`
	Anchor  string `json:"anchor"`  // 第 1 步那根 K 线的时间
	EndTime string `json:"endTime"` // 最后一步那根 K 线的时间
	// StepCount / AudioCount / EventCount 是**摘要里的计数**，名字带 Count 是为了
	// 不和 Recording 里的 Steps（每一步的事件数组）撞 JSON 名 —— 一旦撞了，
	// Go 的内嵌字段会被外层同名字段盖掉，列表里的「12 步」会变成一段 JSON 数组。
	StepCount  int    `json:"stepCount"`  // 共几步（含第 1 步）
	AudioCount int    `json:"audioCount"` // 共几条录音
	EventCount int    `json:"eventCount"` // 共多少个操作（绘图/下单/步进都算）
	Bars       int    `json:"bars"`       // 录像里一共有多少根 K 线
	Size       int64  `json:"size"`       // 录像目录占用（JSON + 音频）
	CreatedBy  string `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
	EndedAt    int64  `json:"endedAt"`
}

// Recording 是一条录像的全部内容。
//
// 它**不是视频**，而是「起始盘面 + 之后每一个操作的原始广播」：
//
//	Base   第 1 步那一刻的完整画布（已揭示的 K 线、标注、挂单、成交、持仓）
//	Steps  第 2 步起，每次【下一根】开一段，段内按顺序排列那一步发生的操作
//
// 回放就是把 Base 深拷贝一份，然后按顺序把事件重新「演」一遍 —— 和实时界面
// 用的是同一套状态更新逻辑（前端 stores/instance-reducer.ts），所以
// **回放看到的画面和当时看到的必然一模一样**，包括机器人做的操作。
type Recording struct {
	RecordingSummary
	Tick      float64 `json:"tick"`
	TickValue float64 `json:"tickValue"`

	Base   RecordingBase   `json:"base"`
	Steps  []RecordingStep `json:"steps"`
	Audios []AudioClip     `json:"audios"`
}

// RecordingBase 是录制起点那一刻的盘面。
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

// RecordingStep 是录像里的一步。
//
// 第 1 步没有事件（它就是 Base 本身，而且不从 bar_advanced 开始）；
// 第 2 步起，Events[0] 一定是那条 bar_advanced —— 正是它把这一步开出来的。
type RecordingStep struct {
	Step   int              `json:"step"`
	Time   string           `json:"time"` // 这一步对应的 K 线时间
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
// 录像内数据被重排也不会错位。Step 是冗余的一份（列表里显示「第几步」用）。
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
}

// RecordingStoppedPayload 是「录制结束」的广播负载。
//
// 录制本身一定已经结束（内存里的录像已清空），Saved 是否为空表示**落盘**成没成：
// 目录没权限、磁盘满之类的情况要说清楚，但不能把已经录完的内容一起丢掉。
type RecordingStoppedPayload struct {
	Recording RecordingSummary `json:"recording"`
	Dir       string           `json:"dir,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// RecordingAudioPayload 是一条录音落位之后的广播。
type RecordingAudioPayload struct {
	Audio AudioClip      `json:"audio"`
	State RecordingState `json:"state"`
}

// RecordingAudioClearedPayload 是「清空当前这一步的录音」的广播。
//
// Removed 里是刚被删掉的几条（文件名要一起带，前端可能正在播其中一条，得停下）。
type RecordingAudioClearedPayload struct {
	Removed []AudioClip  `json:"removed"`
	State   RecordingState `json:"state"`
}

// RecordingsMsg 是录像列表。
type RecordingsMsg struct {
	Type       string             `json:"type"`
	Dir        string             `json:"dir"`
	Recordings []RecordingSummary `json:"recordings"`
}

// RecordingMsg 是一条录像的完整内容。
type RecordingMsg struct {
	Type      string     `json:"type"`
	Recording *Recording `json:"recording"`
}
