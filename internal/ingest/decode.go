// Package ingest 把 WebSocket 帧文本解码成 K 线，决定「这根要不要落盘、写进哪个目录」，再交给 Sink。
//
// 它要回答三个问题，每一个都对应一段被真实数据教出来的规则：
//
//  1. **这帧里有没有 K 线？** 不认方法名，按形状找 { s: [ { i, v: [...] } ] }。
//     顺带一个已实测的结论：报价（qsd）、指标（st）这些负载**不会**被这个形状误抽
//     —— 拿整段真实抓包逐段验过（详见 README）。
//
//  2. **这个周期的数据要不要收？** 只收配置指定的周期（目前只考虑 5 分钟 = 300s）。
//     帧里没有任何字段直接写明周期，所以用**相邻时间戳的众数差**反推。
//     在反推出来之前，这批 K 线先暂存不落盘 —— 宁可晚几百毫秒，
//     也不要把 1 小时图的 K 线混进 5 分钟目录里，那种污染事后很难发现。
//     反推是**按（会话，序列）分别做的**：一个页面上可能同时有好几个图表会话
//     （刷新前的旧会话、多面板布局的各面板），它们的序列键都叫 sds_1，
//     混在一个投票池里会互相污染。
//
//  3. **这根属于哪个标的？** 只能由 `symbol_resolved` 决定（会话 + 品种键 -> pro_name），
//     配合 `create_series` 给出的（序列 -> 品种键）绑定，才能精确到「哪根序列是哪个品种」。
//     页面 URL 的 ?symbol= 只是猜测，兜底用。
//     连猜都猜不了的时候，K 线暂存不落盘 —— 不知道写进哪个目录，硬写就会污染别的标的。
//
// 第 3 条这一版重写过，原因记在这里：旧版在**所有**消息里搜 pro_name，而同一个 WebSocket
// 上还跑着侧边栏/自选/报价面板的 qsd，里面同样带 pro_name，并且是别的品种。
// 实测后果：在 CME_MINI:MNQ1! 的图表页上抓来的 K 线被写进了 NASDAQ_NDX 目录。
package ingest

import (
	"sort"
	"strings"
	"sync"

	"tradingview-j/internal/store"
	"tradingview-j/internal/tvproto"
)

// Sink 是落盘出口（实际实现是 store.Store）
type Sink interface {
	Add(symbol string, b store.Bar)
}

type Options struct {
	IntervalSeconds int64
	Symbol          string // 非空 = 强制指定，忽略帧里的一切品种信息
	Seed            string // 初始猜测（一般来自图表页 URL 的 ?symbol=），只在没有任何权威信息时用
	PendingMax      int    // 暂存上限，防止极端情况下内存无边界增长
	Log             func(format string, args ...any)
	Sink            Sink
}

// seriesKey 唯一标识「某个图表会话里的某根序列」。
//
// 一定要带上会话：一个页面的 WS 上可以同时有多个图表会话（刷新前的旧会话、
// 多面板布局里的各个面板、页面上的迷你图），它们各自的 sds_1 是不同的东西。
type seriesKey struct {
	scope string
	name  string
}

func (k seriesKey) String() string {
	if k.scope == "" {
		return k.name
	}
	return k.scope + "/" + k.name
}

// sessionState 是一个图表会话里关于「谁是谁」的全部知识。
type sessionState struct {
	symbols map[string]string // 品种键 sds_sym_N -> pro_name（来自 symbol_resolved）
	series  map[string]string // 序列键 sds_N   -> 品种键 sds_sym_N（来自 create_series）
	multi   bool              // 已经提示过「这个会话里有多个品种」
}

type Decoder struct {
	opts   Options
	seed   string
	forced bool
	// seedConfirmed 标记「URL 猜的那个品种已经被帧里的 symbol_resolved 确认过」，只用于提示一次
	seedConfirmed bool

	mu        sync.Mutex
	sessions  map[string]*sessionState
	series    map[seriesKey]*seriesStat
	held      map[seriesKey][]store.Bar // 品种还没定下来的 K 线
	ignored   map[string]bool
	lastSym   map[seriesKey]string // 上次归属，用于「改判」提示
	warned    map[seriesKey]bool   // 已经提示过「暂存不猜」的序列
	guessed   map[seriesKey]bool   // 已经提示过「用的是 URL 猜测」的序列
	symBars   map[string]int       // 品种 -> 落盘根数，用来取「主品种」
	foreign   map[string]int       // 页面上出现过的别的品种 -> 被报价的次数（只用于日志）
	foreignOK bool                 // 「别的品种」提示是否已经打过

	st Stats
}

// Stats 是给状态行用的计数
type Stats struct {
	Frames              int64
	RecvFrames          int64
	SendFrames          int64
	JSONSegments        int64
	Heartbeats          int64
	Opaque              int64
	BarEntries          int64 // 按形状抽到的条数
	BarsParsed          int64 // 通过数值校验的 K 线数（含对同一根的重复更新）
	BarsWritten         int64 // 真正交给 Sink 的
	BarsSkippedInterval int64 // 周期不符被判掉的
	BarsHeld            int64 // 暂存中（周期未定或品种未定）
	BarsDropped         int64 // 暂存溢出丢掉的
	Series              map[string]string
	// Foreign / ForeignTotal：页面上出现过的**别的**品种（报价里的），按报价次数从多到少。
	// 它们不参与归属判定，但「页面上有这些品种」是排查「品种认错」的关键线索。
	Foreign      []string
	ForeignTotal int
}

type seriesStat struct {
	votes   map[int64]int
	voted   int
	est     int64
	pending map[int64]store.Bar
}

func New(opts Options) *Decoder {
	if opts.IntervalSeconds <= 0 {
		opts.IntervalSeconds = 300
	}
	if opts.PendingMax <= 0 {
		opts.PendingMax = 20000
	}
	return &Decoder{
		opts:     opts,
		seed:     opts.Seed,
		forced:   opts.Symbol != "",
		sessions: map[string]*sessionState{},
		series:   map[seriesKey]*seriesStat{},
		held:     map[seriesKey][]store.Bar{},
		ignored:  map[string]bool{},
		lastSym:  map[seriesKey]string{},
		warned:   map[seriesKey]bool{},
		guessed:  map[seriesKey]bool{},
		symBars:  map[string]int{},
		foreign:  map[string]int{},
	}
}

func (d *Decoder) logf(format string, args ...any) {
	if d.opts.Log != nil {
		d.opts.Log(format, args...)
	}
}

// Symbol 返回这个页面的「主品种」：落盘 K 线最多的那个。
//
// 一个页面理论上可以产出多个品种（多面板布局），所以状态行与原始帧留档
// 只能挑一个作为代表 —— 挑数据量最大的那个最不容易误导。
// 还没有任何 K 线落盘时退回「页面唯一解析出的品种 / URL 猜测」，只为让日志有个着落。
func (d *Decoder) Symbol() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.forced {
		return d.opts.Symbol
	}
	best, bestN := "", 0
	for sym, n := range d.symBars {
		if n > bestN || (n == bestN && sym < best) {
			best, bestN = sym, n
		}
	}
	if best != "" {
		return best
	}
	if pro := d.onlyPageSymbolLocked(); pro != "" {
		return pro
	}
	return d.seed
}

func (d *Decoder) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.st
	for _, bars := range d.held {
		s.BarsHeld += int64(len(bars))
	}

	// 序列周期：按序列名汇总。同一个名字在不同会话里若反推出不同周期，两个都列出来，
	// 而不是随便挑一个 —— 那正是「多面板」下需要被看见的信息。
	known := map[string]map[string]bool{}
	unknown := map[string]bool{}
	for k, st := range d.series {
		s.BarsHeld += int64(len(st.pending))
		if st.est == 0 {
			unknown[k.name] = true
			continue
		}
		if known[k.name] == nil {
			known[k.name] = map[string]bool{}
		}
		known[k.name][tvproto.IntervalName(st.est)] = true
	}
	s.Series = map[string]string{}
	for name, set := range known {
		s.Series[name] = strings.Join(sortedKeys(set), "|")
	}
	for name := range unknown {
		if _, ok := s.Series[name]; !ok {
			s.Series[name] = "未定"
		}
	}
	if !d.forced {
		s.Foreign, s.ForeignTotal = d.foreignLocked()
	}
	return s
}

// foreignLocked 列出页面上的别的品种，按被报价的次数从多到少
// —— 刷得最勤的那些最可能是「品种认错」的元凶。
func (d *Decoder) foreignLocked() (list []string, total int) {
	chart := d.onlyPageSymbolLocked()
	list = make([]string, 0, len(d.foreign))
	for n := range d.foreign {
		if n == chart {
			continue
		}
		list = append(list, n)
	}
	total = len(list)
	sort.Slice(list, func(i, j int) bool {
		if d.foreign[list[i]] != d.foreign[list[j]] {
			return d.foreign[list[i]] > d.foreign[list[j]]
		}
		return list[i] < list[j]
	})
	return capList(list, foreignShown), total
}

// FeedText 处理一条 WebSocket 帧的负载。
//
// 行情只从 recv 方向取：客户端发上去的帧里没有行情（形状像也不认）。
// 但 send 方向要看一条信令 —— create_series 把「序列」绑到「品种键」。
func (d *Decoder) FeedText(dir, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.st.Frames++
	if dir == "recv" {
		d.st.RecvFrames++
	} else {
		d.st.SendFrames++
	}
	if text == "" {
		return
	}

	if dir != "recv" {
		// 绝大多数上行帧（request_more_data 等）我们不看：先做个子串预筛，省掉解析开销。
		if !strings.Contains(text, "create_series") && !strings.Contains(text, "modify_series") {
			return
		}
		for _, seg := range tvproto.SplitFrame(text) {
			if seg.Kind != tvproto.KindJSON {
				continue
			}
			if scope, series, symKey, ok := tvproto.SeriesBinding(seg.Value); ok {
				d.bindSeriesLocked(scope, series, symKey)
			}
		}
		d.maybeLogForeignLocked()
		return
	}

	for _, seg := range tvproto.SplitFrame(text) {
		switch seg.Kind {
		case tvproto.KindHeartbeat:
			d.st.Heartbeats++
			continue
		case tvproto.KindOpaque:
			d.st.Opaque++
			continue
		}
		d.st.JSONSegments++

		// 品种：只认 symbol_resolved，别处出现的 pro_name 一律不算数。
		if scope, symKey, pro, ok := tvproto.SymbolResolved(seg.Value); ok {
			d.resolveSymbolLocked(scope, symKey, pro)
			continue
		}
		if !d.forced {
			d.noteForeignLocked(seg.Value)
		}

		entries := tvproto.ExtractBars(seg.Value)
		if len(entries) == 0 {
			continue
		}
		scope := tvproto.MessageScope(seg.Value)
		method := seg.Method()
		bySeries := map[string][]store.Bar{}
		for _, e := range entries {
			d.st.BarEntries++
			bar, ok := tvproto.ToBar(e)
			if !ok {
				continue
			}
			bar.Method = method
			d.st.BarsParsed++
			name := bar.Series
			if name == "" {
				name = "(default)"
			}
			bySeries[name] = append(bySeries[name], store.Bar{
				T: bar.T, O: bar.O, H: bar.H, L: bar.L, C: bar.C,
				V: bar.V, HasVolume: bar.HasVolume,
			})
		}
		for name, bars := range bySeries {
			d.acceptSeriesLocked(seriesKey{scope: scope, name: name}, bars)
		}
	}
	d.maybeLogForeignLocked()
}

// ---------- 品种归属 ----------

func (d *Decoder) sessionLocked(scope string) *sessionState {
	ss := d.sessions[scope]
	if ss == nil {
		ss = &sessionState{symbols: map[string]string{}, series: map[string]string{}}
		d.sessions[scope] = ss
	}
	return ss
}

// resolveSymbolLocked 记下「某会话的某品种键 = 某品种」。
func (d *Decoder) resolveSymbolLocked(scope, symbolKey, pro string) {
	if d.forced {
		return
	}
	ss := d.sessionLocked(scope)
	prev, existed := ss.symbols[symbolKey]
	ss.symbols[symbolKey] = pro

	switch {
	case !existed:
		d.logf("识别品种：%s（会话 %s · 品种键 %s）", pro, scope, symbolKey)
	case prev != pro:
		d.logf("品种键 %s 改判：%s -> %s（会话 %s）", symbolKey, prev, pro, scope)
	}
	if d.seed != "" && pro == d.seed && !d.seedConfirmed {
		// 页面 URL 猜的那个品种被帧确认了：值得说一声，用户才知道「当前认的是这个品种」
		// 不是猜的。
		d.seedConfirmed = true
		d.logf("品种已由帧内 symbol_resolved 确认：%s", pro)
	}
	d.noteMultiSymbolLocked(scope, ss)
	d.flushHeldLocked()
}

// noteMultiSymbolLocked 一个会话里解析出多个品种时，把可用信息一次性说清楚。
// 这条日志是排查「品种认错」的关键：告诉用户归属到底按什么判定、什么情况会被暂存。
func (d *Decoder) noteMultiSymbolLocked(scope string, ss *sessionState) {
	if ss.multi {
		return
	}
	pros := map[string]bool{}
	for _, pro := range ss.symbols {
		pros[pro] = true
	}
	if len(pros) < 2 {
		return
	}
	ss.multi = true
	d.logf("会话 %s 上解析出多个品种（%s）—— 归属按「序列 → 品种键」的绑定判定；"+
		"没有绑定的序列会暂存不落盘，不猜", scope, strings.Join(capList(sortedKeys(pros), 3), "、"))
}

func (d *Decoder) bindSeriesLocked(scope, seriesKey, symbolKey string) {
	if d.forced {
		return
	}
	ss := d.sessionLocked(scope)
	if ss.series[seriesKey] == symbolKey {
		return
	}
	ss.series[seriesKey] = symbolKey
	d.flushHeldLocked()
}

// symbolForLocked 决定一根 (会话, 序列) 的 K 线该写进哪个品种目录。
//
// 第二返回值代表「这个结论是不是帧里认出来的」：false 表示是 URL 里的 ?symbol= 猜的。
//
// 判定顺序（越靠前越权威）：
//  1. 这根序列被 create_series 绑到了某个品种键，而该品种键被 symbol_resolved 解析过；
//  2. 这个会话只解析出一个品种 —— 那只能是它；
//  3. 整个页面只解析出一个品种 —— 大概率是它（旧会话、页面刷新等场景靠这条兜住）；
//  4. 什么都没有，只有 URL 里的 ?symbol= —— 猜的，会说一声。
//
// 页面里已经能解析出品种、但这根序列对不上号（同会话多品种且没有绑定）时
// **返回空串**：让它暂存，绝不赌一个。
func (d *Decoder) symbolForLocked(k seriesKey) (string, bool) {
	if d.forced {
		return d.opts.Symbol, true
	}
	if ss := d.sessions[k.scope]; ss != nil {
		if symKey, ok := ss.series[k.name]; ok {
			if pro, ok := ss.symbols[symKey]; ok {
				return pro, true
			}
		}
		if len(ss.symbols) == 1 {
			for _, pro := range ss.symbols {
				return pro, true
			}
		}
	}
	if pro := d.onlyPageSymbolLocked(); pro != "" {
		return pro, true
	}
	if d.anyResolvedLocked() {
		return "", false // 认得出品种，但跟这根序列对不上 —— 宁可不写
	}
	if d.seed != "" {
		return d.seed, false
	}
	return "", false
}

// onlyPageSymbolLocked 返回「整个页面恰好只解析出一个品种」时的那个品种。
func (d *Decoder) onlyPageSymbolLocked() string {
	only := ""
	for _, ss := range d.sessions {
		for _, pro := range ss.symbols {
			if only == "" {
				only = pro
			} else if only != pro {
				return ""
			}
		}
	}
	return only
}

func (d *Decoder) anyResolvedLocked() bool {
	for _, ss := range d.sessions {
		if len(ss.symbols) > 0 {
			return true
		}
	}
	return false
}

// ---------- 落盘 ----------

func (d *Decoder) acceptSeriesLocked(k seriesKey, bars []store.Bar) {
	st := d.series[k]
	if st == nil {
		st = &seriesStat{votes: map[int64]int{}, pending: map[int64]store.Bar{}}
		d.series[k] = st
	}

	// 用本批内部的相邻时间戳差投票。同一批里的 K 线必然同周期，所以
	// 一小批（甚至 du 的单根）也能积累有用的一票。
	ordered := make([]int64, 0, len(bars))
	for _, b := range bars {
		ordered = append(ordered, b.T)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for i := 1; i < len(ordered); i++ {
		if diff := ordered[i] - ordered[i-1]; diff > 0 {
			st.votes[diff]++
			st.voted++
		}
	}
	if st.est == 0 && st.voted >= 2 {
		best, bestN := int64(0), 0
		for diff, n := range st.votes {
			if n > bestN || (n == bestN && diff < best) {
				best, bestN = diff, n
			}
		}
		st.est = best
		key := k.String() + "@" + tvproto.IntervalName(best)
		if best != d.opts.IntervalSeconds && !d.ignored[key] {
			d.ignored[key] = true
			d.logf("序列 %s 的周期是 %s（%ds），不是要收的 %ds —— 该序列的数据一律跳过",
				k, tvproto.IntervalName(best), best, d.opts.IntervalSeconds)
		} else if best == d.opts.IntervalSeconds {
			d.logf("序列 %s 周期确认 %s", k, tvproto.IntervalName(best))
		}
	}

	switch {
	case st.est == 0:
		// 还没反推出周期：暂存，等确认后再决定去留
		for _, b := range bars {
			if len(st.pending) >= d.opts.PendingMax {
				d.st.BarsDropped++
				continue
			}
			st.pending[b.T] = b
		}
	case st.est != d.opts.IntervalSeconds:
		d.st.BarsSkippedInterval += int64(len(bars))
		st.pending = map[int64]store.Bar{}
	default:
		out := make([]store.Bar, 0, len(st.pending)+len(bars))
		for _, b := range st.pending {
			out = append(out, b)
		}
		if len(st.pending) > 0 {
			sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
			st.pending = map[int64]store.Bar{}
		}
		out = append(out, bars...)
		d.emitLocked(k, out)
	}
}

// emitLocked 把 K 线交给 Sink。品种还没确定时先攒着 ——
// 不知道写进哪个文件夹，硬写就会污染别的标的。
func (d *Decoder) emitLocked(k seriesKey, bars []store.Bar) {
	sym, authoritative := d.symbolForLocked(k)
	if sym == "" {
		d.holdLocked(k, bars)
		return
	}
	if !authoritative && !d.guessed[k] {
		d.guessed[k] = true
		d.logf("还没有可用的 symbol_resolved，序列 %s 先用页面 URL 里的 ?symbol= 作为品种：%s"+
			"（拿到权威值后会自动改判；要绝对可控就填 config.json 的 symbol）", k, sym)
	}
	if prev, ok := d.lastSym[k]; ok && prev != sym {
		d.logf("序列 %s 的品种改判：%s -> %s（之后的 K 线写进新目录，已写下的不动）", k, prev, sym)
	}
	d.lastSym[k] = sym

	if d.opts.Sink == nil {
		return
	}
	for _, b := range bars {
		d.opts.Sink.Add(sym, b)
		d.st.BarsWritten++
	}
	d.symBars[sym] += len(bars)
}

func (d *Decoder) holdLocked(k seriesKey, bars []store.Bar) {
	if !d.warned[k] {
		d.warned[k] = true
		if d.anyResolvedLocked() {
			d.logf("序列 %s 对不上任何已解析的品种（同会话里有多张图？）—— 这 %d 根先暂存，不猜", k, len(bars))
		} else {
			d.logf("序列 %s 还没有品种信息（没收到 symbol_resolved，URL 里也没有 ?symbol=）—— 这 %d 根先暂存", k, len(bars))
		}
	}
	for _, b := range bars {
		if len(d.held[k]) >= d.opts.PendingMax {
			d.st.BarsDropped++
			continue
		}
		d.held[k] = append(d.held[k], b)
	}
}

// flushHeldLocked 在「品种知识变了」之后重试暂存的 K 线：能归属的就补写，不能的继续留着。
func (d *Decoder) flushHeldLocked() {
	if len(d.held) == 0 {
		return
	}
	keys := make([]seriesKey, 0, len(d.held))
	for k := range d.held {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, k := range keys {
		if sym, _ := d.symbolForLocked(k); sym == "" {
			continue
		}
		bars := d.held[k]
		delete(d.held, k)
		d.logf("补写品种确定前收到的 %d 根 K 线（%s）", len(bars), k)
		d.emitLocked(k, bars)
	}
}

// ---------- 诊断：页面上还有哪些别的品种 ----------

// noteForeignLocked 记下报价流里出现过的品种名。它们只是被记下来做提示：
// 这些名字曾经把品种判定带跑偏过（见包注释），所以「页面上有它们」这件事值得说出来。
func (d *Decoder) noteForeignLocked(v any) {
	for _, n := range tvproto.QuoteNames(v) {
		d.foreign[n]++
	}
}

// maybeLogForeignLocked 在图表品种认出来之后提示一次。
// 此时可能还有别的品种没开始报价，所以措辞是「目前看到」，完整列表在收工汇总里。
func (d *Decoder) maybeLogForeignLocked() {
	if d.forced || d.foreignOK {
		return
	}
	if d.onlyPageSymbolLocked() == "" {
		return
	}
	others, total := d.foreignLocked()
	if total == 0 {
		return
	}
	d.foreignOK = true
	d.logf("目前看到页面上另有 %d 个品种在走报价（%s%s）—— 它们不是图表品种，不参与归属判定",
		total, strings.Join(others, "、"), suffixMore(total, len(others)))
}

// foreignShown 提示里最多列几个品种名
const foreignShown = 4

// ---------- 小工具 ----------

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func capList(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return list[:n]
}

func suffixMore(total, shown int) string {
	if total <= shown {
		return ""
	}
	return " 等"
}

// SeriesSummary 给状态行用：把 series -> 周期 排好序输出
func (s Stats) SeriesSummary() string {
	if len(s.Series) == 0 {
		return "-"
	}
	names := make([]string, 0, len(s.Series))
	for k := range s.Series {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(n)
		b.WriteString("=")
		b.WriteString(s.Series[n])
	}
	return b.String()
}
