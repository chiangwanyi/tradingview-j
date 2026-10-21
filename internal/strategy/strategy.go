package strategy

// 策略层：只判断「该不该开仓」，**不下单、不管钱、不碰实例**。
//
// 这条分界是刻意的。策略拿到的是一串已经收盘的 K 线 + 当前持仓，返回的是
// 「方向 + 止损价 + 目标价」；真正的下单、挂保护单、日终平仓全在机器人执行器里
// （internal/replay/bot_strategy.go）。好处有三个：
//
//  1. 策略是纯函数，测试里造几根 K 线就能验，不需要起 Hub、连 WS；
//  2. 成交口径只有一份 —— 都走复盘台那套撮合（跳空取开盘价、只撮合上一根之前的单），
//     策略不可能自己「造」一个成交价出来；
//  3. 加第二个策略只需要实现 OnBar，不用碰协议、前端、撮合。
//
// 这个包**不 import replay**：Bar 单独定义一份，由执行器转进来。否则 replay 和
// strategy 互相引用，编译不过。

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bar 是策略看到的 K 线。字段和 CSV 一一对应，价格已经四舍五入到两位小数。
type Bar struct {
	Time   string // "2026-10-16 21:30"，本地时区明文
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Bull / Bear 是最朴素的阴阳判断。开盘等于收盘（十字星）两边都不算。
func (b Bar) Bull() bool { return b.Close > b.Open }
func (b Bar) Bear() bool { return b.Close < b.Open }

// Range 是这根 K 线的振幅（最高 - 最低），不会小于 0。
func (b Bar) Range() float64 {
	if b.High <= b.Low {
		return 0
	}
	return b.High - b.Low
}

// Body 是实体长度。
func (b Bar) Body() float64 {
	if b.Close > b.Open {
		return b.Close - b.Open
	}
	return b.Open - b.Close
}

// ClosePosition 是收盘价在振幅里的位置：0 = 收在最低，1 = 收在最高。
// 振幅为 0 时返回 0.5（没有方向可言，调用方按「不够强」处理）。
func (b Bar) ClosePosition() float64 {
	r := b.Range()
	if r <= 0 {
		return 0.5
	}
	return (b.Close - b.Low) / r
}

// Position 是策略能看到的持仓。**只读**：策略不能改它，也不能自己平仓。
type Position struct {
	Quantity     float64 // 带符号：正 = 多，负 = 空，0 = 空仓
	AveragePrice float64
}

// Side 把带符号的持仓翻成 buy/sell/flat 三种说法。
func (p Position) Side() string {
	switch {
	case p.Quantity > 1e-9:
		return "long"
	case p.Quantity < -1e-9:
		return "short"
	default:
		return "flat"
	}
}

// Context 是策略每次被调用时能看到的**全部**信息。
//
// 注意 Bars 只到 Cursor 为止 —— 执行器只会把已经揭示的 K 线交给策略，
// 后面还没出现的行情在这一层就不可见，策略想用未来函数也用不了。
type Context struct {
	Bars     []Bar
	Cursor   int
	Tick     float64 // 最小跳动，止损价的缓冲和过滤都用它
	Position Position
}

// Bar 返回游标那根（也就是刚收盘的这根）。
func (c *Context) Bar() Bar {
	if c.Cursor < 0 || c.Cursor >= len(c.Bars) {
		return Bar{}
	}
	return c.Bars[c.Cursor]
}

// Prev 取游标往前第 n 根：Prev(1) 是上一根。越界返回 false。
func (c *Context) Prev(n int) (Bar, bool) {
	i := c.Cursor - n
	if i < 0 || i >= len(c.Bars) {
		return Bar{}, false
	}
	return c.Bars[i], true
}

// Len 是已揭示的根数（含当前这根）。
func (c *Context) Len() int { return c.Cursor + 1 }

// Flat 表示空仓。
func (c *Context) Flat() bool { return c.Position.Quantity == 0 }

// Signal 是策略给出的一个开仓意图。
//
// Stop / Target 都是**绝对价格**，不是距离：执行器直接拿它们去挂止损单和限价单，
// 中间不再做任何换算 —— 换算散在两边，迟早有一边把符号或方向写反。
type Signal struct {
	Side   string  `json:"side"`   // buy | sell
	Stop   float64 `json:"stop"`   // 止损触发价（多头在下方、空头在上方）
	Target float64 `json:"target"` // 止盈价（多头在上方、空头在下方）
	Reason string  `json:"reason"` // 一句话说明，写进日志用
}

// Risk 是这笔交易每单位（1 点）承担的风险，用来算 R 倍数。
// 策略已经保证 Stop 在正确的一侧，所以这里恒为正。
func (s Signal) Risk(entry float64) float64 {
	if s.Side == "buy" {
		return entry - s.Stop
	}
	return s.Stop - entry
}

// Param 是一个策略参数的元数据。前端用它在启动面板里显示「这个策略有哪些旋钮」。
//
// 参数一律用 float64：策略的旋钮都是数字（阈值、倍数、根数），没有字符串参数 ——
// 一旦出现「模式」这种枚举，宁可拆成两个参数，也别在协议里加第二套类型。
type Param struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
	Help    string  `json:"help"`
}

// Values 是一组参数值。
type Values map[string]float64

// Float 取值，缺省时用 def。
func (v Values) Float(key string, def float64) float64 {
	if v == nil {
		return def
	}
	x, ok := v[key]
	if !ok {
		return def
	}
	return x
}

// Strategy 是所有策略必须实现的东西。
//
// 生命周期：
//
//	New()            注册表创建实例（此时还没有参数）
//	Configure(vals)  合并参数；执行器启动机器人时调一次
//	OnBar(ctx)       每揭示一根 K 线调用一次 —— **同一根可能被调用多次**，
//	                 实例自己要去重（一般用 ctx.Cursor 记住上次处理到哪）
//	Reset()          回到初始状态（换实例或重新开始跑时）
type Strategy interface {
	ID() string
	Name() string
	Summary() string
	Params() []Param
	Configure(values Values) error
	Reset()
	OnBar(ctx *Context) []Signal
}

// Info 是给前端/协议用的策略描述。
type Info struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Summary string  `json:"summary"`
	Params  []Param `json:"params"`
}

// ---------------- 注册表 ----------------
//
// 「策略的加载」在 Go 里就是这么回事：编译期用 Register 登记一个工厂函数，
// 运行时按 id 取。不做动态 so/dll 加载 —— 那东西在 Windows 上要额外的
// cgo 工具链和版本严格对齐，为了一个复盘工具不值得。

var (
	regMu     sync.RWMutex
	factories = map[string]func() Strategy{}
	regOrder  []string
)

// Register 登记一个策略。重复 id 直接 panic：那是编码错误，
// 让它在上电第一次跑测试时就炸掉，而不是运行时悄悄少一个策略。
func Register(factory func() Strategy) {
	if factory == nil {
		panic("strategy: Register 收到 nil 工厂")
	}
	s := factory()
	id := strings.TrimSpace(s.ID())
	if id == "" {
		panic("strategy: 策略没有 id")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := factories[id]; dup {
		panic("strategy: 策略 id 重复：" + id)
	}
	factories[id] = factory
	regOrder = append(regOrder, id)
}

// List 返回全部策略（按注册顺序，稳定）。前端启动面板直接用这个顺序。
func List() []Info {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Info, 0, len(regOrder))
	for _, id := range regOrder {
		s := factories[id]()
		out = append(out, Info{
			ID:      s.ID(),
			Name:    s.Name(),
			Summary: s.Summary(),
			Params:  s.Params(),
		})
	}
	return out
}

// New 按 id 造一个策略，并把 overrides 合进默认参数。
//
// overrides 里的未知键**不报错也不生效**：前端可能拿着旧版本的参数名发过来，
// 为这个把机器人拦下来没必要。值越界会被夹到 [Min, Max]。
func New(id string, overrides Values) (Strategy, error) {
	regMu.RLock()
	factory, ok := factories[id]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("没有这个策略：%q（可用：%s）", id, strings.Join(IDList(), "、"))
	}
	s := factory()
	if err := s.Configure(Merge(s.Params(), overrides)); err != nil {
		return nil, err
	}
	s.Reset()
	return s, nil
}

// IDList 是全部已注册策略的 id，报错信息和测试用。
func IDList() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, len(regOrder))
	copy(out, regOrder)
	return out
}

// Merge 把默认值和覆盖值合成一份：未知键丢弃，已知键夹到区间内。
func Merge(params []Param, overrides Values) Values {
	out := make(Values, len(params))
	for _, p := range params {
		v := p.Default
		if x, ok := overrides[p.Key]; ok && !isNaN(x) {
			v = x
		}
		if v < p.Min {
			v = p.Min
		}
		if v > p.Max {
			v = p.Max
		}
		out[p.Key] = v
	}
	return out
}

func isNaN(x float64) bool { return x != x }

// ---------------- 时间口径 ----------------
//
// 数据是按**本地自然日**切的文件（00:00~23:55），而 CME 的一个交易日是
// 06:00 开盘、次日 05:00 收盘（05:00~06:00 是结算断档，CSV 里本来就是空的）。
// 所以「哪一天」必须自己算，不能拿文件名当交易日。

// sessionOpenMinute 是 CME 交易日开盘（本地 06:00）的分钟数。
const sessionOpenMinute = 6 * 60

// sessionEndMinute 是「该收摊了」的分界（本地 04:50）。
//
// 取 04:50 而不是 05:00：最后一根是 04:55 开盘的那根，05:00 那根不存在。
// 等到 05:00 再判，最后那根就已经过去了 —— 持仓会被带进下一根（也就是次日 06:00）。
const sessionEndMinute = 4*60 + 50

// Clock 取 "yyyy-MM-dd HH:MM" 里的 HH:MM；格式不对返回空串。
func Clock(t string) string {
	if len(t) < 16 {
		return ""
	}
	return t[11:16]
}

// MinutesOfDay 把 K 线时间换成「当天第几分钟」；解析不出来返回 -1。
func MinutesOfDay(t string) int {
	if len(t) < 16 {
		return -1
	}
	h, m := t[11:13], t[14:16]
	hh, ok1 := twoDigits(h)
	mm, ok2 := twoDigits(m)
	if !ok1 || !ok2 || hh > 23 || mm > 59 {
		return -1
	}
	return hh*60 + mm
}

func twoDigits(s string) (int, bool) {
	if len(s) != 2 {
		return 0, false
	}
	d1, d2 := s[0], s[1]
	if d1 < '0' || d1 > '9' || d2 < '0' || d2 > '9' {
		return 0, false
	}
	return int(d1-'0')*10 + int(d2-'0'), true
}

// TradeDay 返回这根 K 线属于哪个交易日（"2026-10-20"）。
//
// 规则：本地 06:00 之后算当天，06:00 之前算前一天。
// 所以周四 00:00~04:55 那几根属于**周三**的交易日 —— 它们本来就是周三夜盘的尾巴，
// 只是被自然日切到了周四的文件里。
func TradeDay(t string) string {
	if len(t) < 10 {
		return ""
	}
	day := t[:10]
	m := MinutesOfDay(t)
	if m < 0 {
		return day
	}
	if m < sessionOpenMinute {
		d, err := time.ParseInLocation("2006-01-02", day, time.Local)
		if err != nil {
			return day
		}
		return d.AddDate(0, 0, -1).Format("2006-01-02")
	}
	return day
}

// SessionEnd 表示这根 K 线已经进入「收盘收尾」时段：不该再开新仓，有仓位该平掉。
func SessionEnd(t string) bool {
	m := MinutesOfDay(t)
	if m < 0 {
		return false
	}
	return m >= sessionEndMinute && m < sessionOpenMinute
}

// dayCloseMinute 是一个**日历日**里最后一根 5 分钟 K 线的开盘时刻（本地 23:55）。
//
// 它和 sessionEndMinute（04:50）是两回事，别混：
//
//	sessionEndMinute  04:50  CME 结算断档（05:00~05:55 没有数据）之前收摊
//	dayCloseMinute    23:55  **CSV 文件按天切分的边界**，一天里最后一根就是它
//
// 之所以要两条：CSV 一天是本地 00:00~23:55，而一个 CME 交易日是 06:00~次日 05:00，
// 两者错开了 6 小时 —— 一个交易日在文件上被切成两半（前一日 06:00~23:55 +
// 次日 00:00~05:00）。要求「持仓绝不过夜」指的是**文件上的这一夜**，所以判据在 23:55。
const dayCloseMinute = 23*60 + 55

// DayClose 表示这根 K 线是「当天最后一根」：机器人必须在这里把仓位平掉，绝不留过夜。
//
// 用「>= 23:55」而不是「== 23:55」：正常数据最后一根就是 23:55，
// 但万一数据里出现了 23:56/23:59 这种非 5 分钟对齐的行，也不该漏掉。
func DayClose(t string) bool {
	m := MinutesOfDay(t)
	if m < 0 {
		return false
	}
	return m >= dayCloseMinute
}

// CalendarDay 取 "yyyy-MM-dd HH:MM" 里的日期部分 —— **日历日**，不是交易日。
//
// 用来兜住「那一天缺了 23:55 那根」的情况：日历日一变，说明已经跨过午夜了，
// 还有仓位就必须马上平（见 bot_strategy.go 的跨日兜底）。
func CalendarDay(t string) string {
	if len(t) < 10 {
		return ""
	}
	return t[:10]
}

// AlignTickUp / AlignTickDown 把价格对齐到最小跳动的整数倍，方向必须指定。
//
// 为什么不四舍五入：止损/止盈的对齐偏差如果随机分布，一半会让目标更容易打到、
// 一半更难，看起来「无害」；但它其实是**系统性**的 —— 就近取整平均下来，
// 目标会略微变近、止损略微变近，回测结果就悄悄偏乐观了。所以两个方向分开：
//
//	止损  往**更远**对齐（空单向上、多单向下）：宁可多亏一格，也不假装刚好卡在极值
//	止盈  往**更难**对齐（空单向下、多单向上）：宁可少赚一格，也不假装刚好碰到
//
// tick <= 0（标的名认不出来）时原样返回 —— 没有刻度可对齐，硬凑只会引入假精度。
func AlignTickUp(price, tick float64) float64 {
	if tick <= 0 || price <= 0 {
		return price
	}
	// 减去一个极小量：102.25/0.25 在浮点里可能算出 409.0000000000001，
	// 不修的话会被 ceil 推到下一格，止损凭空远了一格。
	return math.Ceil(price/tick-1e-9) * tick
}

func AlignTickDown(price, tick float64) float64 {
	if tick <= 0 || price <= 0 {
		return price
	}
	return math.Floor(price/tick+1e-9) * tick
}

// SortedIDs 只给测试用，保证断言顺序稳定。
func SortedIDs() []string {
	ids := IDList()
	sort.Strings(ids)
	return ids
}
