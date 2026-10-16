package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tradingview-j/internal/store"
)

// storeTestZone 固定成 UTC+8：测试不能依赖跑测机器的本地时区，
// 否则同一份代码换台机器就「红」了，而红的原因和被测逻辑毫无关系。
func storeTestZone() *time.Location { return time.FixedZone("UTC+8", 8*3600) }

const fixtureRel = "../../testdata/real-capture.ndjson"

// mixedFixtureRel 是「页面上同时有别的品种在走报价」的回归样本，
// 详见 internal/tvproto 里对一个测试的说明。
const mixedFixtureRel = "../../testdata/mixed-symbols.ndjson"

// fakeSink 记下每一次落盘调用，用来观察「什么时候写了、写了多少、写给了谁」
type fakeSink struct {
	calls   int
	bySym   map[string]int
	bars    map[int64]store.Bar
	symbols []string
}

func newSink() *fakeSink {
	return &fakeSink{bySym: map[string]int{}, bars: map[int64]store.Bar{}}
}

func (f *fakeSink) Add(symbol string, b store.Bar) {
	f.calls++
	f.bySym[symbol]++
	f.bars[b.T] = b
	if len(f.symbols) == 0 || f.symbols[len(f.symbols)-1] != symbol {
		f.symbols = append(f.symbols, symbol)
	}
}

// collectLogs 把日志收起来给断言用
func collectLogs(dst *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*dst = append(*dst, fmt.Sprintf(format, args...))
	}
}

// frame 给负载套上 ~m~<长度>~m~ 帧头（长度是字节数）
func frame(body string) string { return fmt.Sprintf("~m~%d~m~%s", len(body), body) }

// tsu 造一条 timescale_update：同一个序列的若干根 K 线
func tsu(scope, series string, times ...float64) string {
	items := make([]string, 0, len(times))
	for i, t0 := range times {
		items = append(items, fmt.Sprintf(`{"i":%d,"v":[%v,1,2,0.5,1.5,10]}`, i, t0))
	}
	return frame(fmt.Sprintf(`{"m":"timescale_update","p":["%s",{"%s":{"s":[%s]}}]}`,
		scope, series, strings.Join(items, ",")))
}

// symbolResolved 造一条 symbol_resolved
func symbolResolved(scope, symbolKey, pro string) string {
	return frame(fmt.Sprintf(`{"m":"symbol_resolved","p":["%s","%s",{"pro_name":"%s","source_id":"X"}]}`,
		scope, symbolKey, pro))
}

func feedFixture(t *testing.T, d *Decoder, rel string) {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("打不开样本: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	seen := 0
	for sc.Scan() {
		var rec struct {
			Kind string `json:"k"`
			Dir  string `json:"dir"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("样本坏行: %v", err)
		}
		if rec.Kind != "frame" {
			continue
		}
		seen++
		d.FeedText(rec.Dir, rec.Text)
	}
	if seen == 0 {
		t.Fatalf("样本 %s 里没有帧", rel)
	}
}

func fixtureDecoder(t *testing.T, sink Sink, symbol string) *Decoder {
	t.Helper()
	d := New(Options{IntervalSeconds: 300, Symbol: symbol, Sink: sink, Log: func(string, ...any) {}})
	feedFixture(t, d, fixtureRel)
	return d
}

func TestRealCaptureProducesExpectedBars(t *testing.T) {
	sink := newSink()
	d := fixtureDecoder(t, sink, "")

	if got := d.Symbol(); got != "CME_MINI:MNQ1!" {
		t.Fatalf("品种 = %q，期望从 symbol_resolved 里读出 CME_MINI:MNQ1!", got)
	}
	// 300 个不同时间戳。样本里还有一条 du 属于**另一个图表会话**（cs_gaSTXIAFy41H，
	// 同一次抓包里另一个会话的零星数据）：那边只有 1 根、凑不齐两票、周期反推不出来，
	// 于是按「反推不出来就不落盘」的规则留在暂存里 —— 不再像旧版那样借另一个会话的票放行。
	if len(sink.bars) != 300 {
		t.Fatalf("不同时间戳根数 = %d，期望 300", len(sink.bars))
	}
	if sink.calls != 300 {
		t.Fatalf("落盘调用 = %d，期望 300（另一个会话那根单根仍暂存）", sink.calls)
	}
	if len(sink.bySym) != 1 || sink.bySym["CME_MINI:MNQ1!"] != 300 {
		t.Fatalf("落盘的品种分布 = %v", sink.bySym)
	}

	first := sink.bars[1792021500]
	if first.O != 29932.75 || first.C != 29916.0 || first.V != 8858 {
		t.Fatalf("首根不符: %+v", first)
	}
	last := sink.bars[1792114800]
	if last.C != 30061.5 || last.V != 847 {
		t.Fatalf("末根应取后写覆盖前写的值: %+v", last)
	}

	st := d.Stats()
	if st.Heartbeats != 1 {
		t.Fatalf("心跳段数 = %d，期望 1（样本里有一条 ~m~4~m~~h~1）", st.Heartbeats)
	}
	if st.JSONSegments == 0 || st.Opaque != 0 {
		t.Fatalf("段统计异常: %+v", st)
	}
	if st.BarsSkippedInterval != 0 {
		t.Fatalf("不该有被周期过滤掉的 K 线: %d", st.BarsSkippedInterval)
	}
	if st.Series["sds_1"] != "5m" {
		t.Fatalf("序列周期识别 = %v，期望 sds_1=5m", st.Series)
	}
	// 暂存里正好是另一个会话那 1 根：没有周期信息，一直留着，状态行里能看见
	if st.BarsHeld != 1 {
		t.Fatalf("暂存根数 = %d，期望 1（另一个会话的单根）", st.BarsHeld)
	}
}

func TestHeartbeatFrameIsNotMistakenForJSON(t *testing.T) {
	sink := newSink()
	d := New(Options{IntervalSeconds: 300, Symbol: "X", Sink: sink, Log: func(string, ...any) {}})
	d.FeedText("recv", "~m~4~m~~h~9")
	st := d.Stats()
	if st.Heartbeats != 1 || st.Opaque != 0 || st.JSONSegments != 0 {
		t.Fatalf("心跳被当成别的东西了: %+v", st)
	}
}

func TestSendFramesAreCountedButNotDecoded(t *testing.T) {
	sink := newSink()
	d := New(Options{IntervalSeconds: 300, Symbol: "X", Sink: sink, Log: func(string, ...any) {}})
	// 客户端发出去的帧里也有类似形状的 JSON，但不能被当成行情数据
	d.FeedText("send", `~m~60~m~{"m":"request_more_data","p":[{"s":[{"i":0,"v":[1,2,3,4,5]}]}]}`)
	st := d.Stats()
	if st.SendFrames != 1 || st.JSONSegments != 0 || len(sink.bars) != 0 {
		t.Fatalf("send 方向的帧被解析了: %+v", st)
	}
}

func TestNonMatchingIntervalIsSkipped(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Symbol: "X", Sink: sink, Log: func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
	}})

	// 一小时图的序列：相邻差 3600，必须被整段判掉
	payload := `~m~200~m~{"m":"timescale_update","p":["cs_x",{"sds_9":{"s":[` +
		`{"i":0,"v":[1000,1,2,0.5,1.5,10]},{"i":1,"v":[4600,1,2,0.5,1.5,10]},` +
		`{"i":2,"v":[8200,1,2,0.5,1.5,10]}]}}]}`
	d.FeedText("recv", payload)

	if len(sink.bars) != 0 {
		t.Fatalf("非目标周期的 K 线被落盘了: %v", sink.bars)
	}
	st := d.Stats()
	if st.BarsSkippedInterval != 3 {
		t.Fatalf("跳过计数 = %d，期望 3", st.BarsSkippedInterval)
	}
	if st.Series["sds_9"] != "1h" {
		t.Fatalf("序列周期识别 = %v", st.Series)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "1h") {
		t.Fatalf("应当在日志里说清是周期不符，实际: %s", joined)
	}
}

func TestSingleBarIsHeldUntilIntervalIsKnown(t *testing.T) {
	sink := newSink()
	d := New(Options{IntervalSeconds: 300, Symbol: "X", Sink: sink, Log: func(string, ...any) {}})

	// 第一帧只有一根（du 的典型形态）：没有相邻差，周期还反推不出来
	d.FeedText("recv", `~m~130~m~{"m":"du","p":["cs_x",{"sds_1":{"s":[{"i":0,"v":[300,1,2,0.5,1.5,10]}]}}]}`)
	if len(sink.bars) != 0 {
		t.Fatal("周期未定时不该落盘")
	}
	if held := d.Stats().BarsHeld; held != 1 {
		t.Fatalf("暂存数 = %d，期望 1", held)
	}

	// 第二帧给出多根、周期明确为 300：暂存的那根要跟着一起补上
	d.FeedText("recv", `~m~260~m~{"m":"timescale_update","p":["cs_x",{"sds_1":{"s":[`+
		`{"i":1,"v":[600,1,2,0.5,1.5,10]},{"i":2,"v":[900,1,2,0.5,1.5,10]},{"i":3,"v":[1200,1,2,0.5,1.5,10]}]}}]}`)
	if len(sink.bars) != 4 {
		t.Fatalf("落盘根数 = %d，期望 4（含补写的暂存根）", len(sink.bars))
	}
	if d.Stats().BarsHeld != 0 {
		t.Fatal("暂存应当已清空")
	}
}

func TestBarsBeforeSymbolResolutionAreFlushedLater(t *testing.T) {
	sink := newSink()
	d := New(Options{IntervalSeconds: 300, Sink: sink, Log: func(string, ...any) {}})

	// 先来 K 线、还没有 symbol_resolved，页面 URL 也没提供品种
	d.FeedText("recv", `~m~260~m~{"m":"timescale_update","p":["cs_x",{"sds_1":{"s":[`+
		`{"i":0,"v":[300,1,2,0.5,1.5,10]},{"i":1,"v":[600,1,2,0.5,1.5,10]},{"i":2,"v":[900,1,2,0.5,1.5,10]}]}}]}`)
	if len(sink.bars) != 0 {
		t.Fatal("品种未知时不该落盘（会写进错误的目录）")
	}
	if d.Stats().BarsHeld != 3 {
		t.Fatalf("暂存 = %d，期望 3", d.Stats().BarsHeld)
	}

	// symbol_resolved 到了，之前那 3 根必须补写，而不是丢掉
	d.FeedText("recv", `~m~120~m~{"m":"symbol_resolved","p":["cs_x","sds_sym_1",{"pro_name":"CME_MINI:MNQ1!","name":"MNQ1!"}]}`)
	if len(sink.bars) != 3 {
		t.Fatalf("补写后根数 = %d，期望 3", len(sink.bars))
	}
	if sink.bySym["CME_MINI:MNQ1!"] != 3 {
		t.Fatalf("落盘归属错误: %v", sink.bySym)
	}
}

func TestForcedSymbolIgnoresFrames(t *testing.T) {
	sink := newSink()
	d := New(Options{IntervalSeconds: 300, Symbol: "FORCED:X", Sink: sink, Log: func(string, ...any) {}})
	d.FeedText("recv", `~m~200~m~{"m":"symbol_resolved","p":["cs_x","sds_sym_1",{"pro_name":"CME_MINI:MNQ1!"}]}`)
	d.FeedText("recv", `~m~260~m~{"m":"timescale_update","p":["cs_x",{"sds_1":{"s":[`+
		`{"i":0,"v":[300,1,2,0.5,1.5,10]},{"i":1,"v":[600,1,2,0.5,1.5,10]},{"i":2,"v":[900,1,2,0.5,1.5,10]}]}}]}`)

	if d.Symbol() != "FORCED:X" {
		t.Fatalf("配置里强制指定的品种被帧覆盖了: %q", d.Symbol())
	}
	if len(sink.bySym) != 1 || sink.bySym["FORCED:X"] != 3 {
		t.Fatalf("落盘归属 = %v", sink.bySym)
	}
}

func TestDecoderWritesThroughStore(t *testing.T) {
	// 端到端（无浏览器）：真实抓包 -> 解码 -> CSV 文件
	root := t.TempDir()
	st, err := store.Open(store.Options{Root: root, Header: true, Location: storeTestZone()})
	if err != nil {
		t.Fatal(err)
	}
	d := fixtureDecoder(t, st, "")
	if got := d.Stats().BarsWritten; got != 300 {
		t.Fatalf("写入计数 = %d，期望 300（另一个会话那根单根一直暂存，不落盘）", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "CME_MINI_MNQ1!")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("数据目录不存在: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应当正好两个日文件（跨 10-15 / 10-16），实际 %v", names(entries))
	}
	// 样本区间是 2026-10-14 23:45 ~ 2026-10-16 01:55（UTC）→ +08:00 下跨 10-15 / 10-16
	if got := names(entries); got[0] != "2026-10-15.csv" || got[1] != "2026-10-16.csv" {
		t.Fatalf("日文件名 = %v", got)
	}

	content, err := os.ReadFile(filepath.Join(dir, "2026-10-15.csv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if lines[0] != "time,open,high,low,close,volume" {
		t.Fatalf("表头不符: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "2026-10-15 07:45,29932.75,") {
		t.Fatalf("首行不符: %q", lines[1])
	}
	// 行内时间必须是明文，不能是 Unix 秒
	if strings.Contains(lines[1], "1792021500") {
		t.Fatalf("时间列写成了时间戳: %q", lines[1])
	}
}

func names(es []os.DirEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// ---------- 品种归属：这次重写的重点 ----------

// TestForeignQuoteDoesNotHijackSymbol 是「MNQ1! 的 K 线被写进 NASDAQ_NDX 目录」的回归用例。
//
// 报价消息（qsd）里带 pro_name，而且是别的品种；旧版在所有消息里搜 pro_name，
// 于是一个自选里的 NASDAQ:NDX 就能把图表品种改掉。
func TestForeignQuoteDoesNotHijackSymbol(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Seed: "CME_MINI:MNQ1!", Sink: sink, Log: collectLogs(&logs)})

	// 自选/侧边栏里别的品种的报价（形态来自真实抓包）
	d.FeedText("recv", frame(`{"m":"qsd","p":["qs_multiplexer_full_x",{"n":"NASDAQ:NDX","s":"ok",`+
		`"v":{"subsession_id":"regular","pro_name":"NASDAQ:NDX"}}]}`))
	// 图表自己的报价里甚至夹着 "underlying-symbol":"NASDAQ:NDX"（微纳指跟踪 NDX）
	d.FeedText("recv", frame(`{"m":"qsd","p":["qs_multiplexer_full_x",{"n":"CME_MINI:MNQ1!","s":"ok",`+
		`"v":{"underlying-symbol":"NASDAQ:NDX","pro_name":"CME_MINI:MNQ1!"}}]}`))

	d.FeedText("recv", symbolResolved("cs_x", "sds_sym_1", "CME_MINI:MNQ1!"))
	d.FeedText("recv", tsu("cs_x", "sds_1", 300, 600, 900))

	if got := d.Symbol(); got != "CME_MINI:MNQ1!" {
		t.Fatalf("品种被报价带跑偏了: %q", got)
	}
	if len(sink.bySym) != 1 || sink.bySym["CME_MINI:MNQ1!"] != 3 {
		t.Fatalf("落盘归属 = %v", sink.bySym)
	}
	// 页面上有别的品种在走报价这件事，日志里要说出来 —— 不然用户无从理解
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "NASDAQ:NDX") || !strings.Contains(joined, "不参与归属判定") {
		t.Fatalf("日志里应当说明另有别的品种在走报价:\n%s", joined)
	}
}

// TestMultiSymbolSessionHoldsBarsUntilBindingArrives：同一个图表会话里出现多个品种时，
// 靠 create_series 的「序列 -> 品种键」绑定才能分清谁是谁；没有绑定就暂存，绝不赌一个。
func TestMultiSymbolSessionHoldsBarsUntilBindingArrives(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Sink: sink, Log: collectLogs(&logs)})

	d.FeedText("recv", symbolResolved("cs_x", "sds_sym_1", "CME_MINI:MNQ1!"))
	d.FeedText("recv", symbolResolved("cs_x", "sds_sym_2", "CME_MINI:ES1!"))
	d.FeedText("recv", tsu("cs_x", "sds_1", 300, 600, 900))

	if len(sink.bars) != 0 {
		t.Fatalf("对不上号时不该落盘: %v", sink.bySym)
	}
	if held := d.Stats().BarsHeld; held != 3 {
		t.Fatalf("暂存 = %d，期望 3", held)
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "多个品种") {
		t.Fatalf("应当提示会话里有多个品种:\n%s", joined)
	}

	// 客户端把 sds_1 绑到了 sds_sym_1 —— 之前暂存的那 3 根补写
	d.FeedText("send", frame(`{"m":"create_series","p":["cs_x","sds_1","s1","sds_sym_1","5",300,""]}`))
	if len(sink.bySym) != 1 || sink.bySym["CME_MINI:MNQ1!"] != 3 {
		t.Fatalf("绑定后归属 = %v", sink.bySym)
	}
	if d.Stats().BarsHeld != 0 {
		t.Fatal("暂存应当已清空")
	}

	// 另一根序列绑到另一个品种：各归各的
	d.FeedText("send", frame(`{"m":"create_series","p":["cs_x","sds_2","s1","sds_sym_2","5",300,""]}`))
	d.FeedText("recv", tsu("cs_x", "sds_2", 300, 600, 900))
	if sink.bySym["CME_MINI:ES1!"] != 3 {
		t.Fatalf("第二个序列的归属 = %v", sink.bySym)
	}
}

// TestIntervalIsTrackedPerSession：周期反推按（会话，序列）分别做。
// 同一个页面上两个会话都叫 sds_1，一个 5m 一个 1h —— 混在一个投票池里做多数决的话，
// 1h 那批会被 5m 的票放行，直接污染 5 分钟目录。
func TestIntervalIsTrackedPerSession(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Sink: sink, Log: collectLogs(&logs)})

	d.FeedText("recv", symbolResolved("cs_a", "sds_sym_1", "CME_MINI:MNQ1!"))
	d.FeedText("recv", symbolResolved("cs_b", "sds_sym_1", "BINANCE:BTCUSDT.P"))
	d.FeedText("recv", tsu("cs_a", "sds_1", 300, 600, 900))
	d.FeedText("recv", tsu("cs_b", "sds_1", 1000, 4600, 8200))

	if sink.bySym["CME_MINI:MNQ1!"] != 3 {
		t.Fatalf("5m 会话的数据没落盘: %v", sink.bySym)
	}
	if sink.bySym["BINANCE:BTCUSDT.P"] != 0 {
		t.Fatalf("1h 会话的数据被错放了: %v", sink.bySym)
	}
	st := d.Stats()
	if st.BarsSkippedInterval != 3 {
		t.Fatalf("跳过计数 = %d，期望 3", st.BarsSkippedInterval)
	}
	// 同名序列在不同会话里推出不同周期时，两个都要显示出来，别藏
	if st.Series["sds_1"] != "1h|5m" {
		t.Fatalf("序列周期显示 = %v，期望 sds_1=1h|5m（两个会话各一个）", st.Series)
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "cs_b/sds_1") {
		t.Fatalf("周期不符的日志里应当带上会话，便于区分两个 sds_1:\n%s", joined)
	}
}

// TestURLSeedIsOnlyAGuessAndGetsCorrected：URL 里的 ?symbol= 只是猜测。
// 猜的时候要说一声；事后拿到权威品种就改判，已经写下的不动。
func TestURLSeedIsOnlyAGuessAndGetsCorrected(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Seed: "NASDAQ:NDX", Sink: sink, Log: collectLogs(&logs)})

	d.FeedText("recv", tsu("cs_x", "sds_1", 300, 600, 900))
	if sink.bySym["NASDAQ:NDX"] != 3 {
		t.Fatalf("只有 URL 猜测时应当按猜测落盘（宁可有据可查，也别丢数据）: %v", sink.bySym)
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "?symbol=") {
		t.Fatalf("用猜测落盘时必须说明这是猜的:\n%s", joined)
	}

	d.FeedText("recv", symbolResolved("cs_x", "sds_sym_1", "CME_MINI:MNQ1!"))
	d.FeedText("recv", tsu("cs_x", "sds_1", 1200, 1500, 1800))
	if sink.bySym["CME_MINI:MNQ1!"] != 3 {
		t.Fatalf("拿到权威品种后应当改判: %v", sink.bySym)
	}
	if got := d.Symbol(); got != "CME_MINI:MNQ1!" {
		t.Fatalf("主品种应当是数据量大的那个: %q", got)
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "改判") {
		t.Fatalf("改判时应当说清楚:\n%s", joined)
	}
}

// TestMixedSymbolFixtureStaysOnChartSymbol：整段真实抓包（页面上另有 10 个品种在走报价）
// 走一遍解码器，不提供 URL 兜底，全靠协议判定 —— 必须只认图表会话里那一个品种。
func TestMixedSymbolFixtureStaysOnChartSymbol(t *testing.T) {
	sink := newSink()
	logs := []string{}
	d := New(Options{IntervalSeconds: 300, Sink: sink, Log: collectLogs(&logs)})
	feedFixture(t, d, mixedFixtureRel)

	if len(sink.bySym) != 1 {
		t.Fatalf("只应当产出一个品种，实际 %v", sink.bySym)
	}
	// 独立统计的期望值：timescale_update 300 根 + 三条 du 各 1 根（同一时间戳的更新）
	if sink.bySym["CME_MINI:MNQ1!"] != 303 {
		t.Fatalf("落盘根数 = %v，期望 CME_MINI:MNQ1!=303", sink.bySym)
	}
	if len(sink.bars) != 300 {
		t.Fatalf("不同时间戳 = %d，期望 300", len(sink.bars))
	}
	if got := d.Symbol(); got != "CME_MINI:MNQ1!" {
		t.Fatalf("主品种 = %q", got)
	}
	if held := d.Stats().BarsHeld; held != 0 {
		t.Fatalf("样本里所有序列都有绑定，不该有暂存: %d", held)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "另有") {
		t.Fatalf("日志里应当报出「页面上另有别的品种在走报价」:\n%s", joined)
	}
	// 完整名单（按报价次数排序）里必须有那个把旧版本带跑偏的 NASDAQ:NDX
	st := d.Stats()
	if st.ForeignTotal != 9 {
		t.Fatalf("别的品种数 = %d，期望 9（独立统计：10 个报价品种里减去图表品种）", st.ForeignTotal)
	}
	if len(st.Foreign) == 0 || st.Foreign[0] != "NASDAQ:NDX" {
		t.Fatalf("别的品种名单 = %v，期望 NASDAQ:NDX 排最前（它被报价 5 次，最多）", st.Foreign)
	}
}
