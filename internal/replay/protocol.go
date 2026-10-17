package replay

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
	ReqOrderPlace    = "order_place"
	ReqOrderCancel   = "order_cancel"
	ReqBotStart      = "bot_start"
	ReqBotStop       = "bot_stop"
	ReqPing          = "ping"
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
	Kind  string `json:"kind"` // line | rect | text
	Start *Point `json:"start"`
	End   *Point `json:"end"`
	Text  string `json:"text"`
	Color string `json:"color"`
	Snap  bool   `json:"snap"` // true = 端点吸附到该 K 线的 OHLC（前端按住 Ctrl）

	// annotation_move / annotation_delete
	AnnotationID string `json:"annotationId"`

	// order_place
	Side     string  `json:"side"` // buy | sell
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`

	// order_cancel
	OrderID string `json:"orderId"`

	// bot_start
	IntervalMs int `json:"intervalMs"`
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
)

// 广播的动作名（Event.Action）。
const (
	ActBarAdvanced      = "bar_advanced"
	ActAnnotationAdded  = "annotation_added"
	ActAnnotationMoved  = "annotation_moved"
	ActAnnotationDelete = "annotation_deleted"
	ActOrderPlaced      = "order_placed"
	ActOrderCancelled   = "order_cancelled"
	ActControlChanged   = "control_changed"
	ActMembersChanged   = "members_changed"
	ActBotStarted       = "bot_started"
	ActBotStopped       = "bot_stopped"
)

// Point 是标注的一个端点：横坐标是 K 线时间字符串，纵坐标是价格。
//
// 横坐标**必须是已揭示数据里某一根 K 线的时间**：标注是画在历史上的，
// 指向一根还不存在的 K 线没有意义（也会提前泄露后面的行情）。
type Point struct {
	Time  string  `json:"time"`
	Price float64 `json:"price"`
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

// Annotation 是画在画布上的一个图形。三种都用「起点/终点」表示，
// 文字只用 Start（锚在文字左下角），End 留零值。
type Annotation struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // line | rect | text
	Start     Point  `json:"start"`
	End       Point  `json:"end"`
	Text      string `json:"text,omitempty"`
	Color     string `json:"color,omitempty"`
	Snapped   bool   `json:"snapped"` // 端点吸附过 OHLC（界面上画小圆点提示）
	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
}

// 订单状态。
const (
	OrderOpen      = "open"
	OrderFilled    = "filled"
	OrderCancelled = "cancelled"
)

// 订单类型。
const (
	KindLimit = "limit"
	KindStop  = "stop"
)

// Order 是一张挂单。没有止损/止盈类型：用户想止损就自己挂一张 stop 单。
type Order struct {
	ID          string  `json:"id"`
	Side        string  `json:"side"` // buy | sell
	Kind        string  `json:"kind"` // limit | stop
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
	Tick          float64      `json:"tick"` // 最小跳动，价格轴按它定小数位
	Bars          []Bar        `json:"bars"`
	HiddenBars    int          `json:"hiddenBars"` // 已加载但还没揭示的根数
	HasMore       bool         `json:"hasMore"`    // 数据目录里还有没有更晚的日文件
	Annotations   []Annotation `json:"annotations"`
	Orders        []Order      `json:"orders"`
	Trades        []Trade      `json:"trades"`
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
}

// ---------------- 广播负载 ----------------

// BarAdvancedPayload 是推进一根 K 线之后的结果。
// 订单/成交/持仓每次都发全量：它们数量很少（一次复盘通常个位数），
// 全量发送让前端不用维护「增量合并」这套容易出错的逻辑。
type BarAdvancedPayload struct {
	Bar           Bar      `json:"bar"`
	Cursor        int      `json:"cursor"`
	HiddenBars    int      `json:"hiddenBars"`
	HasMore       bool     `json:"hasMore"`
	Orders        []Order  `json:"orders"`
	Trades        []Trade  `json:"trades"`
	Position      Position `json:"position"`
	RealizedPnl   float64  `json:"realizedPnl"`
	UnrealizedPnl float64  `json:"unrealizedPnl"`
	Fills         []Trade  `json:"fills"` // 这一根 K 线触发成交的单（可能为空）
}

type AnnotationPayload struct {
	Annotation Annotation `json:"annotation"`
}

type AnnotationDeletedPayload struct {
	AnnotationID string `json:"annotationId"`
}

type OrdersPayload struct {
	Orders []Order `json:"orders"`
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
