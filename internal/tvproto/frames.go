// Package tvproto 解 TradingView 图表页的 WebSocket 协议。
//
// 帧的外层是自定义分帧：~m~<长度>~m~<负载>，负载通常是 JSON，也可能是心跳 ~h~<n>。
// 一个 WebSocket 帧里可以串接多段（实测一帧里有 10 段：series_loading / symbol_resolved /
// qsd×5 / quote_completed / timescale_update / series_completed）。
//
// 这里刻意**不硬编码方法名**去取数据，而是「按形状找」：
// 任何形状为 { s: [ { i, v: [数值...] }, ... ] } 的节点都当成一串 K 线。
// 这样 du / timescale_update / 将来新增的方法名都能吃到，协议改版时不容易碎。
package tvproto

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// 段落种类
const (
	KindJSON      = "json"
	KindHeartbeat = "heartbeat"
	KindOpaque    = "opaque"
)

type Segment struct {
	Kind     string
	Raw      string
	Header   string
	Declared int
	Value    any // Kind == KindJSON 时的解析结果（可能是对象/数组/数字）
}

// Method 返回 {"m":"du"} 里的方法名，拿不到返回空串。
func (s Segment) Method() string {
	if m, ok := s.Value.(map[string]any); ok {
		if name, ok := m["m"].(string); ok {
			return name
		}
	}
	return ""
}

// SplitFrame 把一段帧文本切成消息列表。正向扫描而不是「按 ~m~ 头切分后取间隙」：
// 因为心跳（~h~<n>）没有 ~m~ 头，服务端会把它直接贴在 JSON 负载后面，
// 用间隙法会把 `{"m":...}~h~7` 当成一段，导致 JSON 解析失败。
func SplitFrame(text string) []Segment {
	var segs []Segment
	pos := 0
	for pos < len(text) {
		if strings.HasPrefix(text[pos:], "~h~") {
			end := pos + 3
			for end < len(text) && text[end] >= '0' && text[end] <= '9' {
				end++
			}
			n, _ := strconv.Atoi(text[pos+3 : end])
			segs = append(segs, Segment{Kind: KindHeartbeat, Raw: text[pos:end], Declared: n})
			pos = end
			continue
		}

		if declared, headLen, ok := parseHeader(text, pos); ok {
			headerStart := pos
			payloadStart := pos + headLen
			end := payloadStart + declared
			if end > len(text) {
				end = len(text)
			}
			// 声明长度的语义（字节还是字符）不保证一致，负载里也可能含非 ASCII。
			// 对不上时退化成「切到下一个帧头之前」—— 宁可多吞一点，也不丢数据。
			// 「负载长度必须等于声明长度」这一条不能省：声明长度超出剩余文本时，
			// 只判「切到头了」会把后面几段一起吞进来（踩过，回归用例已锁）。
			consistent := end-payloadStart == declared && (end == len(text) || hasHeaderAt(text, end))
			if !consistent {
				if next := nextHeader(text, payloadStart); next >= 0 {
					end = next
				} else {
					end = len(text)
				}
			}
			// end 恒 >= payloadStart > headerStart，所以循环一定前进
			payload := text[payloadStart:end]
			pos = end
			if payload != "" {
				segs = append(segs, makeSegment(payload, text[headerStart:payloadStart]))
			}
			continue
		}

		// 既不是心跳头也不是消息头：吞到下一个帧头之前，作为无法解析的原始片段保留
		next := nextHeader(text, pos)
		if next < 0 {
			next = len(text)
		}
		if next > pos {
			segs = append(segs, Segment{Kind: KindOpaque, Raw: text[pos:next]})
		}
		pos = next
	}
	return segs
}

// makeSegment 的 header 参数是原始帧头文本（~m~123~m~），仅用于诊断。
func makeSegment(payload, header string) Segment {
	// 心跳有两种出现形式，实测都见过：
	//   ~h~1          裸心跳（直接贴在上一段负载后面）
	//   ~m~4~m~~h~1   把心跳当成负载、长度声明为 4
	// 第二种如果不在这里识别出来，就会被当成「无法解析的 JSON」，心跳计数也就丢了。
	if strings.HasPrefix(payload, "~h~") {
		end := 3
		for end < len(payload) && payload[end] >= '0' && payload[end] <= '9' {
			end++
		}
		n, _ := strconv.Atoi(payload[3:end])
		return Segment{Kind: KindHeartbeat, Raw: payload, Header: header, Declared: n}
	}
	seg := Segment{Kind: KindJSON, Raw: payload, Header: header}
	var v any
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		seg.Kind = KindOpaque
		return seg
	}
	seg.Value = v
	return seg
}

func parseHeader(text string, pos int) (declared, headLen int, ok bool) {
	if pos+3 > len(text) || !strings.HasPrefix(text[pos:], "~m~") {
		return 0, 0, false
	}
	i := pos + 3
	start := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == start || i+3 > len(text) || text[i:i+3] != "~m~" {
		return 0, 0, false
	}
	n, err := strconv.Atoi(text[start:i])
	if err != nil {
		return 0, 0, false
	}
	return n, i + 3 - pos, true
}

func hasHeaderAt(text string, pos int) bool {
	return strings.HasPrefix(text[pos:], "~m~") || strings.HasPrefix(text[pos:], "~h~")
}

func nextHeader(text string, from int) int {
	a := strings.Index(text[from:], "~m~")
	b := strings.Index(text[from:], "~h~")
	switch {
	case a < 0 && b < 0:
		return -1
	case a < 0:
		return from + b
	case b < 0:
		return from + a
	case a < b:
		return from + a
	default:
		return from + b
	}
}

// Entry 是从 JSON 里按形状捞出来的「一串 K 线」中的一条原始条目
type Entry struct {
	Series    string
	Index     float64
	Values    []float64 // [t,o,h,l,c,v...]，非数值位置填 NaN
	Timescale string
}

// Bar 是校验通过、可以直接落盘的 K 线
type Bar struct {
	T          int64 // Unix 秒（UTC），是这根 K 线的开盘时间
	O, H, L, C float64
	V          float64
	HasVolume  bool
	Series     string
	Method     string
}

// ExtractBars 从任意 JSON 里按形状抽 K 线。
//
// 注意 lbs.bar_close_time **不是**这根 K 线的收盘时间。实测整段抓包里它只有一个取值
// （图表右侧可视边界），拿它减 t 会得到随时间线性漂移的垃圾周期。
// 所以周期只能靠相邻时间戳的众数差反推（见 ingest 包），这里不碰它。
func ExtractBars(v any) []Entry {
	var out []Entry
	walk(v, "", &out)
	return out
}

func walk(node any, series string, out *[]Entry) {
	switch n := node.(type) {
	case []any:
		for _, c := range n {
			walk(c, series, out)
		}
	case map[string]any:
		if arr, ok := n["s"].([]any); ok {
			ts, _ := n["t"].(string)
			for _, it := range arr {
				item, ok := it.(map[string]any)
				if !ok {
					continue
				}
				vals, ok := item["v"].([]any)
				if !ok {
					continue
				}
				e := Entry{Series: series, Timescale: ts}
				for _, x := range vals {
					if f, ok := toFloat(x); ok {
						e.Values = append(e.Values, f)
					} else {
						e.Values = append(e.Values, math.NaN())
					}
				}
				if f, ok := toFloat(item["i"]); ok {
					e.Index = f
				}
				*out = append(*out, e)
			}
		}
		for k, v := range n {
			// v/s/ns/lbs 是 K 线自身的字段，不是子序列容器，不能往下钻
			if k == "v" || k == "s" || k == "ns" || k == "lbs" {
				continue
			}
			switch child := v.(type) {
			case map[string]any:
				next := series
				if _, has := child["s"].([]any); has {
					next = k // 形如 {"sds_1": {"s": [...]}}：系列名就是键名
				}
				walk(child, next, out)
			case []any:
				walk(child, series, out)
			}
		}
	}
}

// ToBar 做数值校验：[t,o,h,l,c,(v)] 至少 5 个有限数。
func ToBar(e Entry) (Bar, bool) {
	if len(e.Values) < 5 {
		return Bar{}, false
	}
	t, o, h, l, c := e.Values[0], e.Values[1], e.Values[2], e.Values[3], e.Values[4]
	for _, x := range []float64{t, o, h, l, c} {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return Bar{}, false
		}
	}
	b := Bar{T: int64(math.Round(t)), O: o, H: h, L: l, C: c, Series: e.Series}
	if len(e.Values) > 5 && !math.IsNaN(e.Values[5]) && !math.IsInf(e.Values[5], 0) {
		b.V = e.Values[5]
		b.HasVolume = true
	}
	return b, true
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// SymbolFromURL 从图表页 URL 的 ?symbol= 里读品种名（兜底手段）。
func SymbolFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("symbol")
}

// ---------- 会话与品种：只有这两条信令能决定「K 线写进哪个目录」 ----------
//
// 一个图表页的 WebSocket 上跑的不只是图表数据：侧边栏/自选/报价面板的 qsd、
// 指标（study）、还有该页面上其他图表会话的数据，全在同一条连接上。
// 报价消息里**同样**带 pro_name，而且是别的品种 —— 所以在所有消息里找 pro_name
// 是错的（踩过：MNQ1! 的 K 线被写进了 NASDAQ_NDX 目录，详见 README）。
//
// 正确做法是认协议语义：
//
//	recv {"m":"symbol_resolved","p":["cs_I5hYBRNGkSRt","sds_sym_1",{...,"pro_name":"CME_MINI:MNQ1!",...}]}
//	                                                   ↑会话              ↑品种键
//	send {"m":"create_series","p":["cs_I5hYBRNGkSRt","sds_1","s1","sds_sym_1","5",300,""]}
//	                                                   ↑序列键        ↑品种键
//	recv {"m":"timescale_update","p":["cs_I5hYBRNGkSRt",{"sds_1":{"s":[...]}}]}
//	                                    ↑会话
//
// 于是「这根 K 线属于哪个品种」= 数据消息的会话 + 序列名 → 品种键 → pro_name。

// MessageScope 返回消息第一个参数里的会话 id（图表会话 cs_xxx / 报价会话 qs_xxx）。
// 拿不到返回空串；空串也当作用域用 —— 至少不会跨会话串味。
func MessageScope(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	p, ok := m["p"].([]any)
	if !ok || len(p) == 0 {
		return ""
	}
	s, _ := p[0].(string)
	return s
}

// SymbolResolved 解析品种解析消息，返回 （会话, 品种键, pro_name）。
//
// 这是**唯一**可信的品种来源。注意 pro_name 是在这条消息的负载里搜到的，
// 不是在整帧里搜 —— 区别就是上面说的那个 bug。
func SymbolResolved(v any) (scope, symbolKey, proName string, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap || m["m"] != "symbol_resolved" {
		return "", "", "", false
	}
	p, isArr := m["p"].([]any)
	if !isArr || len(p) < 3 {
		return "", "", "", false
	}
	scope, _ = p[0].(string)
	symbolKey, _ = p[1].(string)
	proName = findProName(p[2])
	if scope == "" || symbolKey == "" || proName == "" {
		return "", "", "", false
	}
	return scope, symbolKey, proName, true
}

// SeriesBinding 解析「把某根序列绑到某个品种键」的信令，返回（会话, 序列键, 品种键）。
//
// create_series 是客户端发上去的（实测形状见包注释）。modify_series 在抓包里没出现过，
// 这里按同样的参数顺序试着解析 —— **这一条是推断，不是实测**；形状对不上时这里的
// 校验（四个位置都得是字符串）会让它解析失败，退化成「按会话判定」而不是乱配。
//
// 有了绑定才能回答：同一个会话里出现多个品种时，sds_1 到底是哪一个。
func SeriesBinding(v any) (scope, seriesKey, symbolKey string, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return "", "", "", false
	}
	switch m["m"] {
	case "create_series", "modify_series":
	default:
		return "", "", "", false
	}
	p, isArr := m["p"].([]any)
	if !isArr || len(p) < 4 {
		return "", "", "", false
	}
	scope, ok0 := p[0].(string)
	seriesKey, ok1 := p[1].(string)
	_, ok2 := p[2].(string)
	symbolKey, ok3 := p[3].(string)
	if !ok0 || !ok1 || !ok2 || !ok3 || scope == "" || seriesKey == "" || symbolKey == "" {
		return "", "", "", false
	}
	return scope, seriesKey, symbolKey, true
}

// QuoteNames 返回报价消息（qsd）自称在报哪个品种。
//
// 只为日志诊断存在：页面上到底有多少别的品种在走报价，是解释「品种为什么会认错」
// 的关键线索。绝不参与归属判定。
func QuoteNames(v any) []string {
	m, isMap := v.(map[string]any)
	if !isMap || m["m"] != "qsd" {
		return nil
	}
	p, isArr := m["p"].([]any)
	if !isArr || len(p) < 2 {
		return nil
	}
	item, isMap2 := p[1].(map[string]any)
	if !isMap2 {
		return nil
	}
	name, _ := item["n"].(string)
	if !looksLikeSymbolName(name) {
		return nil
	}
	return []string{name}
}

// looksLikeSymbolName 把「品种名」和自选里那种 `={"adjustment":"splits",...}` 伪键区分开：
// 真品种名形如 CME_MINI:MNQ1! / BINANCE:BTCUSDT.P，不含引号、等号、花括号，也不会很长。
func looksLikeSymbolName(name string) bool {
	if name == "" || len(name) > 40 || !strings.Contains(name, ":") {
		return false
	}
	return !strings.ContainsAny(name, "=\"{}\\")
}

// findProName 在任意 JSON 节点里找 pro_name（只在 symbol_resolved 的负载里用）。
func findProName(node any) string {
	switch n := node.(type) {
	case []any:
		for _, c := range n {
			if s := findProName(c); s != "" {
				return s
			}
		}
	case map[string]any:
		if s, ok := n["pro_name"].(string); ok && s != "" {
			return s
		}
		for _, c := range n {
			if s := findProName(c); s != "" {
				return s
			}
		}
	}
	return ""
}

var intervalNames = map[int64]string{
	1: "1s", 5: "5s", 15: "15s", 30: "30s",
	60: "1m", 120: "2m", 180: "3m", 300: "5m", 600: "10m", 900: "15m", 1800: "30m",
	3600: "1h", 7200: "2h", 14400: "4h", 86400: "1d", 604800: "1w", 2592000: "1M",
}

// IntervalName 只用于日志可读性（5m / 1h 比 300 / 3600 好认）
func IntervalName(sec int64) string {
	if name, ok := intervalNames[sec]; ok {
		return name
	}
	if sec <= 0 {
		return "?"
	}
	return strconv.FormatInt(sec, 10) + "s"
}
