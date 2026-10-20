// Package capture 是程序的常驻主体：等 Chrome、等图表页、把帧喂给解码器、把 K 线落进 CSV。
//
// 行为约定（需求里明确的流程）：
//
//	启动程序 -> 自动等待 Chrome 的调试端口 -> 自动等待 tradingview 图表页出现 ->
//	用户随便拖图表/回放 -> 帧被旁路收下来 -> 解码成 5 分钟 K 线 -> 覆盖写当日 CSV
//
// 三个刻意的取舍：
//
//   - **只被动监听，绝不主动操作页面。** 不拖图表、不刷新、不注入脚本。
//     用户手动操作图表时页面自己会去请求历史，程序老实记录即可 —— 这样对
//     TradingView 而言没有任何「非人类」的交互序列。
//   - **连不上就等，不退出。** 端口没开、页面没开、浏览器没登录，都不是错误状态，
//     而是「还没到时候」。每轮只打印一次原因，剩下的交给状态行。
//   - **一个匹配的页面都不放过。** 同时开好几个图表标签页时每个都挂上监听，
//     各自独立的解码器与留档文件，互不干扰。
package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tradingview-j/internal/cdp"
	"tradingview-j/internal/config"
	"tradingview-j/internal/ingest"
	"tradingview-j/internal/logx"
	"tradingview-j/internal/rawlog"
	"tradingview-j/internal/store"
	"tradingview-j/internal/tvproto"
	"tradingview-j/internal/wsclient"
)

// rawPendingBytesMax 品种还没确定时，原始帧在内存里最多攒这么多字节。
// 超了就先落到 (unknown) 目录 —— 丢帧比写错目录更难补救。
const rawPendingBytesMax = 16 << 20

type Session struct {
	cfg *config.Config
	st  *store.Store
	lg  *logx.Logger

	snap     atomic.Value // Snapshot
	waiting  bool         // 本轮连接是否已经提示过「在等页面」
	lastDiag string       // 上一次打印过的发现失败诊断（同一条不重复刷屏）
}

func New(cfg *config.Config, st *store.Store, lg *logx.Logger) *Session {
	s := &Session{cfg: cfg, st: st, lg: lg}
	s.snap.Store(Snapshot{})
	return s
}

// ---------- 状态快照（供状态行读取） ----------

type PageStatus struct {
	Title          string
	URL            string
	Symbol         string
	Frames         int64
	Recv           int64
	Send           int64
	Heartbeats     int64
	BarsWritten    int64
	BarsHeld       int64
	Skipped        int64
	Bytes          int64
	Series         string
	LastFrameAt    time.Time
	RawFile        string
	RawUnavailable bool

	// ForeignQuotes 是页面上「别的品种」的报价（不参与归属判定，只用于解释品种认得对不对）
	ForeignQuotes []string
	ForeignTotal  int
}

type Snapshot struct {
	Connected bool
	Endpoint  string
	Pages     []PageStatus
}

func (s *Session) Snapshot() Snapshot {
	v, _ := s.snap.Load().(Snapshot)
	return v
}

func (s *Session) storeSnap(v Snapshot) { s.snap.Store(v) }

// ---------- 主循环 ----------

func (s *Session) Run(ctx context.Context) error {
	go s.statusLoop(ctx)

	maxFrame := int64(s.cfg.MaxFrameMB) << 20
	for {
		if ctx.Err() != nil {
			return nil
		}
		ep, err := cdp.Discover(cdp.DiscoverOptions{Port: s.cfg.CDPPort})
		if err != nil {
			s.reportDiscoverFailure(err)
			if !sleepCtx(ctx, s.cfg.RetryEvery()) {
				return nil
			}
			continue
		}
		s.lastDiag = ""
		s.lg.Info("发现浏览器调试端点：%s", ep.String())

		tr, err := cdp.DialTransport(ep.BrowserWSURL, wsclient.Options{
			Timeout:    25 * time.Second,
			MaxPayload: maxFrame,
		})
		if err != nil {
			s.lg.Warn("连不上调试端点：%v", err)
			s.lg.Warn("如果 Chrome 弹出了「Allow remote debugging?」对话框，请在浏览器里点 Allow（每个浏览器会话都要点一次）")
			if !sleepCtx(ctx, s.cfg.RetryEvery()) {
				return nil
			}
			continue
		}

		s.lg.Info("CDP 通道已建立，开始等待 TradingView 图表页…")
		s.storeSnap(Snapshot{Connected: true, Endpoint: ep.String()})
		s.supervise(ctx, tr, maxFrame)
		tr.Close()
		s.storeSnap(Snapshot{})

		if ctx.Err() != nil {
			return nil
		}
		s.lg.Warn("CDP 连接已断开，%s 后重试", s.cfg.RetryEvery())
		if !sleepCtx(ctx, s.cfg.RetryEvery()) {
			return nil
		}
	}
}

// reportDiscoverFailure 只在诊断内容变化时打印全文，避免每 5 秒刷一屏同样的东西。
func (s *Session) reportDiscoverFailure(err error) {
	msg := err.Error()
	if msg == s.lastDiag {
		return
	}
	s.lastDiag = msg
	s.lg.Warn("还没发现 Chrome 调试端点。请确认：")
	s.lg.Plain("     1) Chrome 已打开（用你平时那个已登录 TradingView 的窗口）")
	s.lg.Plain("     2) 地址栏访问 chrome://inspect/#remote-debugging")
	s.lg.Plain("     3) 勾选「Allow remote debugging for this browser instance」")
	s.lg.Plain("     4) 勾选后每次外部连接，Chrome 会弹框要求点 Allow —— 点了才通")
	s.lg.Plain("   诊断：%s", strings.ReplaceAll(msg, "\n", "\n         "))
	s.lg.Info("%s 后自动重试", s.cfg.RetryEvery())
}

// supervise 在一次 CDP 连接的生命周期内工作，返回即代表连接断了。
func (s *Session) supervise(ctx context.Context, tr *cdp.Transport, maxFrame int64) {
	reg := newRegistry()
	defer reg.closeAll(s.lg)

	tr.OnEvent(func(sessionID, method string, params json.RawMessage) {
		// 这个回调跑在 CDP 读协程里，只能做「快而纯」的事：
		// 解码 + 写内存。任何 Call 都会自己等自己，绝不能出现在这里。
		switch method {
		case "Network.webSocketFrameReceived":
			if p := reg.get(sessionID); p != nil {
				p.onFrame("recv", params)
			}
		case "Network.webSocketFrameSent":
			if p := reg.get(sessionID); p != nil {
				p.onFrame("send", params)
			}
		}
	})

	needles := []string{s.cfg.MatchURL}
	poll := time.NewTicker(s.cfg.AttachPoll())
	defer poll.Stop()
	flush := time.NewTicker(s.cfg.FlushEvery())
	defer flush.Stop()

	s.waiting = false
	s.pollTargets(ctx, tr, reg, needles)
	s.publish(reg)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tr.Done():
			return
		case <-flush.C:
			if err := s.st.Flush(); err != nil {
				// store 内部已经把具体文件与原因打出来了
				s.lg.Warn("有文件这轮没写成功，下个周期会重试")
			}
			reg.flushRaw()
			s.publish(reg)
		case <-poll.C:
			if !s.pollTargets(ctx, tr, reg, needles) {
				return
			}
			s.publish(reg)
		}
	}
}

func (s *Session) pollTargets(ctx context.Context, tr *cdp.Transport, reg *registry, needles []string) bool {
	if ctx.Err() != nil {
		return false
	}
	targets, err := tr.GetTargets(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.lg.Warn("读取标签页列表失败：%v", err)
		}
		return false
	}

	live := map[string]cdp.Target{}
	for _, t := range targets {
		if cdp.IsPage(t) {
			live[t.ID] = t
		}
	}

	// 1) 清理已经不存在的页面
	for _, p := range reg.list() {
		if _, ok := live[p.targetID]; ok {
			continue
		}
		s.lg.Info("图表页已关闭：%s", p.describe())
		reg.remove(p.sessionID)
		tr.Detach(ctx, p.sessionID)
	}

	// 2) 接入新出现的匹配页面
	matched := 0
	for _, t := range live {
		if cdp.MatchTarget([]cdp.Target{t}, needles) == nil {
			continue
		}
		matched++
		if reg.hasTarget(t.ID) {
			continue
		}
		s.attachPage(ctx, tr, reg, t)
	}

	// 3) 一页都没匹配到：提示一次，之后交给状态行
	if matched == 0 && !s.waiting {
		s.waiting = true
		s.lg.Warn("还没找到 TradingView 图表页。请在 Chrome 里打开图表页（URL 里带 %q）", s.cfg.MatchURL)
		s.lg.Plain("     程序会一直等着，打开后自动接入，不需要重启。")
	}
	if matched > 0 {
		s.waiting = false
	}
	return true
}

func (s *Session) attachPage(ctx context.Context, tr *cdp.Transport, reg *registry, t cdp.Target) {
	if reg.recentlyFailed(t.ID) {
		return
	}
	sid, err := tr.Attach(ctx, t.ID)
	if err != nil {
		reg.markFailed(t.ID)
		s.lg.Warn("接不进这个页面：%s", t.URL)
		s.lg.Warn("  原因：%v", err)
		s.lg.Plain("     最常见的原因是「这个标签页上已经打开了开发者工具」——")
		s.lg.Plain("     一个标签页同时只能有一个调试会话，关掉 DevTools 后本程序会自动重试。")
		return
	}

	// 先登记、再打开网络事件 —— 顺序不能反。
	//
	// EnableNetwork 的响应后面紧跟着的就是页面自己的 WebSocket 流量，而事件分发那边
	// 只认**已经登记过**的 session（supervise 里的 reg.get(sessionID)）。先 enable
	// 后登记的写法有一个窗口：帧已经到了，页面还没进注册表，这几帧被静默丢掉。
	// 真实浏览器里这个窗口表现成「刚接上的一瞬间丢几帧」；在只推三帧的
	// TestEndToEndWithoutBrowser 里直接表现成「一行 CSV 都没写出来」——
	// 本机实测在全量跑时约半数概率碰上（2026-10 修的）。
	p := newPage(s.cfg, s.st, s.lg, t, sid)
	reg.add(p)
	if err := tr.EnableNetwork(ctx, sid, s.cfg.NetworkBufferMB); err != nil {
		reg.remove(sid)
		reg.markFailed(t.ID)
		tr.Detach(ctx, sid)
		s.lg.Warn("打开网络事件失败：%v", err)
		return
	}

	s.lg.Info("已接入图表页：%s", p.describe())
	if sym := p.dec.Symbol(); sym != "" {
		s.lg.Info("  当前品种：%s（若帧里带 symbol_resolved，会以它为准）", sym)
	}
	s.lg.Info("  现在可以在 Chrome 里随意拖动图表或使用回放，K 线会被自动写入 %s", s.cfg.OutPath())
}

func (s *Session) publish(reg *registry) {
	snap := Snapshot{Connected: true}
	if v, ok := s.snap.Load().(Snapshot); ok {
		snap.Endpoint = v.Endpoint
	}
	for _, p := range reg.list() {
		snap.Pages = append(snap.Pages, p.status())
	}
	sort.Slice(snap.Pages, func(i, j int) bool { return snap.Pages[i].Title < snap.Pages[j].Title })
	s.storeSnap(snap)
}

// ---------- 状态行 ----------

func (s *Session) statusLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.StatusEvery())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.lg.Status("%s", s.statusText())
		}
	}
}

func (s *Session) statusText() string {
	snap := s.Snapshot()
	stats := s.st.Stats()

	var parts []string
	if !snap.Connected {
		parts = append(parts, "未连上 Chrome（等待调试开关）")
	} else if len(snap.Pages) == 0 {
		parts = append(parts, "已连上 Chrome，等待 TradingView 图表页")
	} else {
		for _, p := range snap.Pages {
			seg := fmt.Sprintf("页[%s] 帧 %d(收%d/发%d) 心跳 %d 数据 %.1fMB",
				trim(p.Title, 18), p.Frames, p.Recv, p.Send, p.Heartbeats, float64(p.Bytes)/(1<<20))
			if p.Symbol != "" {
				seg += " 品种 " + p.Symbol
			}
			seg += " 周期 " + orDash(p.Series)
			parts = append(parts, seg)
		}
	}
	for _, st := range stats {
		parts = append(parts, fmt.Sprintf("%s %d 根/%d 天（推送 %d 次）", st.Folder, st.Bars, st.Days, st.Accepted))
	}
	return "[状态] " + strings.Join(parts, " · ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---------- 页面（一个匹配的图表标签页 = 一个 page） ----------

type page struct {
	cfg *config.Config
	st  *store.Store
	lg  *logx.Logger

	targetID  string
	sessionID string
	title     string
	url       string

	mu           sync.Mutex
	dec          *ingest.Decoder
	raw          *rawlog.Writer
	rawPath      string
	rawPending   []rawlog.Record
	rawPendingSz int
	rawGaveUp    bool
	frames       int64
	recv         int64
	send         int64
	bytes        int64
	lastFrameAt  time.Time
	closed       bool
}

func newPage(cfg *config.Config, st *store.Store, lg *logx.Logger, t cdp.Target, sessionID string) *page {
	p := &page{
		cfg:       cfg,
		st:        st,
		lg:        lg,
		targetID:  t.ID,
		sessionID: sessionID,
		title:     t.Title,
		url:       t.URL,
	}
	p.dec = ingest.New(ingest.Options{
		IntervalSeconds: cfg.IntervalSeconds,
		Symbol:          cfg.Symbol,
		Seed:            tvproto.SymbolFromURL(t.URL),
		Sink:            st,
		Log: func(format string, args ...any) {
			lg.Info("[%s] %s", p.shortName(), fmt.Sprintf(format, args...))
		},
	})
	return p
}

func (p *page) shortName() string {
	if p.title != "" {
		return trim(p.title, 18)
	}
	return "页"
}

func (p *page) describe() string {
	return fmt.Sprintf("%s  %s", trim(p.title, 40), p.url)
}

// onFrame 处理一条 WebSocket 帧事件。
//
// 所有帧事件都来自同一条 CDP 读协程，所以这里天然串行；
// 加锁是为了与「监督协程关闭本页面」互斥。
func (p *page) onFrame(dir string, params json.RawMessage) {
	var ev struct {
		RequestID string  `json:"requestId"`
		Timestamp float64 `json:"timestamp"`
		Response  struct {
			Opcode      int    `json:"opcode"`
			PayloadData string `json:"payloadData"`
		} `json:"response"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	payload := ev.Response.PayloadData
	if payload == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}

	p.frames++
	p.bytes += int64(len(payload))
	p.lastFrameAt = time.Now()
	if dir == "recv" {
		p.recv++
	} else {
		p.send++
	}

	if p.cfg.SaveRawFrames {
		rec := rawlog.Record{
			Kind:      "frame",
			At:        int64(ev.Timestamp * 1000),
			EventAt:   time.Now().UnixMilli(),
			RequestID: ev.RequestID,
			Dir:       dir,
			Opcode:    ev.Response.Opcode,
			Len:       len(payload),
		}
		if ev.Response.Opcode == 2 {
			rec.B64 = payload // 二进制帧按 base64 保真存，保证解码期不丢信息
		} else {
			rec.Text = payload
		}
		p.writeRawLocked(rec)
	}

	// 只有文本帧拿去解码；二进制帧在留档里已经保真存下了
	text := payload
	if ev.Response.Opcode != 1 {
		text = ""
	}
	p.dec.FeedText(dir, text)
}

func (p *page) writeRawLocked(rec rawlog.Record) {
	if p.rawGaveUp {
		return
	}
	if p.raw == nil {
		p.ensureRawLocked() // 品种已知就开文件
		if p.raw == nil && p.rawPendingSz+recSize(rec) > rawPendingBytesMax {
			p.openRawLocked("(unknown)")
		}
	}
	if p.raw == nil {
		p.rawPending = append(p.rawPending, rec)
		p.rawPendingSz += recSize(rec)
		return
	}
	if err := p.raw.Write(rec); err != nil {
		p.lg.Warn("写原始帧留档失败：%v", err)
	}
}

func recSize(rec rawlog.Record) int { return len(rec.Text) + len(rec.B64) }

func (p *page) ensureRawLocked() {
	if sym := p.dec.Symbol(); sym != "" {
		p.openRawLocked(sym)
	}
}

func (p *page) openRawLocked(symbol string) {
	folder := p.st.Folder(symbol)
	w, err := rawlog.New(p.cfg.RawPath(), folder, time.Now())
	if err != nil {
		p.lg.Warn("开不了原始帧留档：%v（K 线数据不受影响）", err)
		p.rawGaveUp = true
		return
	}
	p.raw = w
	p.rawPath = w.Path()
	// 头一条写 meta：留档文件要能自己说明「它是从哪个页面、哪个品种来的」。
	// 不看文件夹名也能对上号，把文件单独拷走也不会失去上下文。
	_ = w.Write(rawlog.Record{
		Kind:     "meta",
		App:      "tradingview-j",
		At:       time.Now().UnixMilli(),
		TargetID: p.targetID,
		Title:    p.title,
		URL:      p.url,
		Symbol:   symbol,
		Source:   p.sessionID,
	})
	if len(p.rawPending) > 0 {
		for _, rec := range p.rawPending {
			w.Write(rec)
		}
		p.lg.Info("[%s] 品种确定后才开留档文件，补写 %d 条积压帧", p.shortName(), len(p.rawPending))
		p.rawPending = nil
		p.rawPendingSz = 0
	}
	p.lg.Info("[%s] 原始帧留档：%s", p.shortName(), p.rawPath)
}

func (p *page) flushRaw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.raw != nil {
		p.raw.Flush()
	}
}

func (p *page) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	if p.raw != nil {
		if err := p.raw.Close(); err != nil {
			p.lg.Warn("关闭留档文件失败：%v", err)
		}
	}
}

func (p *page) status() PageStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.dec.Stats()
	return PageStatus{
		Title:          p.title,
		URL:            p.url,
		Symbol:         p.dec.Symbol(),
		Frames:         p.frames,
		Recv:           p.recv,
		Send:           p.send,
		Heartbeats:     st.Heartbeats,
		BarsWritten:    st.BarsWritten,
		BarsHeld:       st.BarsHeld,
		Skipped:        st.BarsSkippedInterval,
		Bytes:          p.bytes,
		Series:         st.SeriesSummary(),
		LastFrameAt:    p.lastFrameAt,
		RawFile:        p.rawPath,
		RawUnavailable: p.rawGaveUp,
		ForeignQuotes:  st.Foreign,
		ForeignTotal:   st.ForeignTotal,
	}
}

// ---------- 页面注册表 ----------

type registry struct {
	mu    sync.Mutex
	pages map[string]*page // sessionId -> page
	fail  map[string]time.Time
}

func newRegistry() *registry {
	return &registry{pages: map[string]*page{}, fail: map[string]time.Time{}}
}

func (r *registry) get(sessionID string) *page {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pages[sessionID]
}

func (r *registry) add(p *page) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages[p.sessionID] = p
}

func (r *registry) remove(sessionID string) *page {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pages[sessionID]
	delete(r.pages, sessionID)
	return p
}

func (r *registry) hasTarget(targetID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pages {
		if p.targetID == targetID {
			return true
		}
	}
	return false
}

func (r *registry) list() []*page {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*page, 0, len(r.pages))
	for _, p := range r.pages {
		out = append(out, p)
	}
	return out
}

func (r *registry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pages)
}

// recentlyFailed 让「接不进去」的页面在一分钟内不再重试 ——
// 否则每 3 秒往终端刷一次同样的警告，把真正有用的日志顶掉。
func (r *registry) recentlyFailed(targetID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.fail[targetID]; ok && time.Since(t) < time.Minute {
		return true
	}
	return false
}

func (r *registry) markFailed(targetID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail[targetID] = time.Now()
}

func (r *registry) flushRaw() {
	for _, p := range r.list() {
		p.flushRaw()
	}
}

// closeAll 收工汇总。除了帧数与落盘根数，还要把「页面上还有哪些别的品种在走报价」
// 说一句 —— 这些品种曾经把品种归属带跑偏过（见 ingest 包注释），
// 在收工这一行里露脸，用户才有机会发现自己看到的目录名对不对。
func (r *registry) closeAll(lg *logx.Logger) {
	for _, p := range r.list() {
		st := p.status()
		if st.Frames > 0 {
			line := fmt.Sprintf("收工：%s · 帧 %d · 落盘 K 线 %d 根（含覆盖），暂存 %d 根",
				p.shortName(), st.Frames, st.BarsWritten, st.BarsHeld)
			if st.ForeignTotal > 0 {
				line += fmt.Sprintf(" · 页面上另有 %d 个品种的报价（%s%s）",
					st.ForeignTotal, strings.Join(st.ForeignQuotes, "、"), moreSuffix(st.ForeignTotal, len(st.ForeignQuotes)))
			}
			lg.Info("%s", line)
		}
		p.close()
	}
	r.mu.Lock()
	r.pages = map[string]*page{}
	r.mu.Unlock()
}

func moreSuffix(total, shown int) string {
	if total <= shown {
		return ""
	}
	return " 等"
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
