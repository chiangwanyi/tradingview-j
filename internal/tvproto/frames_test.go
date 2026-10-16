package tvproto

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixturePath 是从真实抓包（2026-10-16，CME_MINI:MNQ1! 5 分钟图）里裁剪出来的回归样本。
// 裁剪规则：meta + 一条携带完整 300 根历史的 timescale_update + 一条 du 增量。
const fixtureRel = "../../testdata/real-capture.ndjson"

func TestSplitFrameSeparatesConcatenatedMessages(t *testing.T) {
	// 心跳没有 ~m~ 头，服务端会直接贴在 JSON 负载后面。
	// 用「按 ~m~ 切分取间隙」的写法会把 `{"m":...}~h~7` 当成一段，JSON 解析随即失败。
	text := `~m~16~m~{"m":"du","p":[]}~h~7~m~11~m~{"m":"x","p"`
	segs := SplitFrame(text)
	if len(segs) != 3 {
		t.Fatalf("段数 = %d, 期望 3: %+v", len(segs), segs)
	}
	if segs[0].Kind != KindJSON || segs[0].Method() != "du" {
		t.Fatalf("第 1 段应为 du，实际 %+v", segs[0])
	}
	if segs[1].Kind != KindHeartbeat || segs[1].Raw != "~h~7" {
		t.Fatalf("第 2 段应为心跳 ~h~7，实际 %+v", segs[1])
	}
	if segs[2].Kind != KindOpaque || segs[2].Raw != `{"m":"x","p"` {
		t.Fatalf("第 3 段应为被截断的原始片段，实际 %+v", segs[2])
	}
}

func TestSplitFrameFallsBackWhenDeclaredLengthIsWrong(t *testing.T) {
	// 声明长度故意写错（和真实抓包里那 146 段一样对不上）。
	// 此时必须退化成「切到下一个帧头之前」，把两段都完整吐出来，而不是把第二段吞掉。
	text := `~m~999~m~{"a":1}~m~7~m~{"b":2}`
	segs := SplitFrame(text)
	if len(segs) != 2 {
		t.Fatalf("段数 = %d, 期望 2: %+v", len(segs), segs)
	}
	if segs[0].Raw != `{"a":1}` || segs[1].Raw != `{"b":2}` {
		t.Fatalf("负载切错: %q / %q", segs[0].Raw, segs[1].Raw)
	}
}

func TestSplitFrameHandlesEmptyLength(t *testing.T) {
	// 长度为 0 的段必须被吃掉，否则扫描器原地打转
	segs := SplitFrame(`~m~0~m~~m~9~m~{"z":true}`)
	if len(segs) != 1 || segs[0].Raw != `{"z":true}` {
		t.Fatalf("结果: %+v", segs)
	}
}

func TestExtractBarsIgnoresIndicatorSeries(t *testing.T) {
	// st1/st2 是指标序列（用 st 键），qsd 是报价；都不能被当成 K 线。
	payload := map[string]any{
		"m": "du",
		"p": []any{
			"cs_x",
			map[string]any{
				"st1": map[string]any{"st": []any{map[string]any{"i": 0, "v": []any{1.0, 2.0}}}},
				"q":   map[string]any{"v": []any{1.0}},
			},
		},
	}
	if got := ExtractBars(payload); len(got) != 0 {
		t.Fatalf("指标/报价不应被抽成 K 线，实际 %+v", got)
	}
}

func TestExtractBarsKeepsSeriesName(t *testing.T) {
	payload := map[string]any{
		"m": "du",
		"p": []any{
			"cs_x",
			map[string]any{
				"sds_2": map[string]any{
					"s": []any{map[string]any{"i": 7, "v": []any{1792114800.0, 1.0, 2.0, 3.0, 4.0, 5.0}}},
					"t": "s1",
				},
			},
		},
	}
	got := ExtractBars(payload)
	if len(got) != 1 {
		t.Fatalf("条目数 = %d, 期望 1", len(got))
	}
	if got[0].Series != "sds_2" {
		t.Fatalf("Series = %q, 期望 sds_2（系列名应当是它上一层的键名）", got[0].Series)
	}
	if got[0].Index != 7 {
		t.Fatalf("Index = %v, 期望 7", got[0].Index)
	}
	bar, ok := ToBar(got[0])
	if !ok || bar.T != 1792114800 || bar.C != 4.0 || !bar.HasVolume || bar.V != 5.0 {
		t.Fatalf("ToBar 结果 %+v ok=%v", bar, ok)
	}
}

func TestToBarRejectsIncompleteValues(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
	}{
		{"只有 4 个数（缺 close）", []float64{1, 2, 3, 4}},
		{"长度不够", []float64{1, 2}},
		{"含 NaN", []float64{1, 2, 3, 4, nan()}},
	}
	for _, c := range cases {
		if _, ok := ToBar(Entry{Values: c.vals}); ok {
			t.Fatalf("%s: 不该通过校验", c.name)
		}
	}
}

func nan() float64 { var f float64; return f / f }

func TestSymbolFromURL(t *testing.T) {
	got := SymbolFromURL("https://cn.tradingview.com/chart/EuR5QG29/?symbol=CME_MINI%3AMNQ1%21")
	if got != "CME_MINI:MNQ1!" {
		t.Fatalf("SymbolFromURL = %q", got)
	}
	if SymbolFromURL("https://cn.tradingview.com/chart/EuR5QG29/") != "" {
		t.Fatal("没有 ?symbol= 时应当返回空串")
	}
}

func TestSymbolResolvedParsesSessionAndSymbolKey(t *testing.T) {
	payload := map[string]any{
		"m": "symbol_resolved",
		"p": []any{"cs_x", "sds_sym_1", map[string]any{
			"source_id": "CME_MINI", "name": "MNQ1!", "pro_name": "CME_MINI:MNQ1!",
		}},
	}
	scope, symKey, pro, ok := SymbolResolved(payload)
	if !ok || scope != "cs_x" || symKey != "sds_sym_1" || pro != "CME_MINI:MNQ1!" {
		t.Fatalf("SymbolResolved = %q %q %q ok=%v", scope, symKey, pro, ok)
	}
}

func TestSymbolResolvedIgnoresOtherMessages(t *testing.T) {
	// 报价消息里也有 pro_name，而且是**别的**品种。这是实测到的真实形态：
	// 自选/侧边栏的 qsd 一到，旧版本就把图表品种改成了 NASDAQ:NDX。
	quote := map[string]any{
		"m": "qsd",
		"p": []any{"qs_multiplexer_full_x", map[string]any{
			"n": "NASDAQ:NDX", "s": "ok",
			"v": map[string]any{"pro_name": "NASDAQ:NDX", "subsession_id": "regular"},
		}},
	}
	if _, _, pro, ok := SymbolResolved(quote); ok {
		t.Fatalf("报价消息不该被当成品种解析，却给出了 %q", pro)
	}

	// 图表数据消息里也没有品种解析结果，但它的负载里可能夹着别的品种名。
	// 实测：MNQ1! 的报价里就带着 "underlying-symbol":"NASDAQ:NDX"（微纳指本来就跟踪 NDX）。
	data := map[string]any{
		"m": "timescale_update",
		"p": []any{"cs_x", map[string]any{
			"sds_1": map[string]any{
				"s": []any{map[string]any{"i": 0, "v": []any{1.0, 2.0, 3.0, 4.0, 5.0}}},
			},
			"underlying-symbol": "NASDAQ:NDX",
		}},
	}
	if _, _, pro, ok := SymbolResolved(data); ok {
		t.Fatalf("数据消息不该给出品种，却给出了 %q", pro)
	}
}

func TestSymbolResolvedRejectsMalformed(t *testing.T) {
	cases := map[string]any{
		"缺会话":        map[string]any{"m": "symbol_resolved", "p": []any{"", "sds_sym_1", map[string]any{"pro_name": "A:B"}}},
		"缺品种键":       map[string]any{"m": "symbol_resolved", "p": []any{"cs_x", "", map[string]any{"pro_name": "A:B"}}},
		"缺 pro_name": map[string]any{"m": "symbol_resolved", "p": []any{"cs_x", "sds_sym_1", map[string]any{"name": "B"}}},
		"参数不够":       map[string]any{"m": "symbol_resolved", "p": []any{"cs_x", "sds_sym_1"}},
	}
	for name, payload := range cases {
		if _, _, pro, ok := SymbolResolved(payload); ok {
			t.Fatalf("%s：不该解析成功（%q）", name, pro)
		}
	}
}

func TestSeriesBindingParsesCreateSeries(t *testing.T) {
	payload := map[string]any{
		"m": "create_series",
		"p": []any{"cs_I5hYBRNGkSRt", "sds_1", "s1", "sds_sym_1", "5", 300.0, ""},
	}
	scope, series, symKey, ok := SeriesBinding(payload)
	if !ok || scope != "cs_I5hYBRNGkSRt" || series != "sds_1" || symKey != "sds_sym_1" {
		t.Fatalf("SeriesBinding = %q %q %q ok=%v", scope, series, symKey, ok)
	}

	// 报价相关信令不能被当成序列绑定
	quote := map[string]any{"m": "quote_add_symbols", "p": []any{"qs_x", "CME_MINI:MNQ1!", "NASDAQ:NDX"}}
	if _, _, _, ok := SeriesBinding(quote); ok {
		t.Fatal("quote_add_symbols 不该被当成序列绑定")
	}
	// 形状对不上（第三个位置不是字符串）时也要拒绝，别乱配
	weird := map[string]any{"m": "create_series", "p": []any{"cs_x", "sds_1", map[string]any{}, "sds_sym_1"}}
	if _, _, _, ok := SeriesBinding(weird); ok {
		t.Fatal("形状不符时不该解析成功")
	}
}

func TestMessageScope(t *testing.T) {
	if got := MessageScope(map[string]any{"m": "du", "p": []any{"cs_x", map[string]any{}}}); got != "cs_x" {
		t.Fatalf("MessageScope = %q", got)
	}
	if got := MessageScope(map[string]any{"m": "du"}); got != "" {
		t.Fatalf("没有 p 时应返回空串，实际 %q", got)
	}
	if got := MessageScope("不是对象"); got != "" {
		t.Fatalf("非对象应返回空串，实际 %q", got)
	}
}

func TestQuoteNames(t *testing.T) {
	got := QuoteNames(map[string]any{"m": "qsd", "p": []any{"qs_x", map[string]any{"n": "NASDAQ:NDX", "s": "ok"}}})
	if len(got) != 1 || got[0] != "NASDAQ:NDX" {
		t.Fatalf("QuoteNames = %v", got)
	}
	// 自选里那种 "={...}" 伪品种键不是品种名
	if got := QuoteNames(map[string]any{"m": "qsd", "p": []any{"qs_x", map[string]any{"n": `={"symbol":"CME_MINI:MNQ1!"}`}}}); len(got) != 0 {
		t.Fatalf("伪品种键被当成了品种名: %v", got)
	}
	if got := QuoteNames(map[string]any{"m": "du", "p": []any{"cs_x", map[string]any{"n": "NASDAQ:NDX"}}}); len(got) != 0 {
		t.Fatalf("非报价消息不该给出名字: %v", got)
	}
}

// ---- 真实抓包回归 ----
//
// 期望值不是用 Go 代码跑出来再抄回去的，而是先用 Python 独立统计原始抓包得到的
// （见 README「测试」一节）。用被测代码生成期望值等于没测。

type rawRecord struct {
	Kind string `json:"k"`
	Dir  string `json:"dir"`
	Text string `json:"text"`
}

func readFixture(t *testing.T) []rawRecord {
	t.Helper()
	return readFixtureFile(t, fixtureRel)
}

func TestRealCaptureDecodesExpectedBars(t *testing.T) {
	recs := readFixture(t)

	var bars []Bar
	symbols := map[string]string{} // 会话/品种键 -> pro_name
	segments, heartbeats := 0, 0

	for _, r := range recs {
		if r.Kind != "frame" {
			continue
		}
		for _, seg := range SplitFrame(r.Text) {
			switch seg.Kind {
			case KindHeartbeat:
				heartbeats++
			case KindJSON:
				segments++
				if scope, symKey, pro, ok := SymbolResolved(seg.Value); ok {
					symbols[scope+"/"+symKey] = pro
				}
				method := seg.Method()
				for _, e := range ExtractBars(seg.Value) {
					bar, ok := ToBar(e)
					if !ok {
						continue
					}
					bar.Method = method
					bars = append(bars, bar)
				}
			}
		}
	}

	if segments == 0 {
		t.Fatal("样本里一段 JSON 都没解出来")
	}
	if heartbeats == 0 {
		t.Fatal("样本里没有心跳段，说明分帧把它们吞进 JSON 了")
	}
	// 样本里只有一条 symbol_resolved，会话与品种键是独立数出来的
	if len(symbols) != 1 || symbols["cs_LL6mfyBnSGZL/sds_sym_1"] != "CME_MINI:MNQ1!" {
		t.Fatalf("没能从 symbol_resolved 里认出品种，实际 %v", symbols)
	}

	// 该样本里同一个 t 出现两次：du 在前（c=30057.75），timescale_update 在后（c=30061.5）。
	// 后写覆盖前写，所以最终 300 根不同时间戳。
	dedup := map[int64]Bar{}
	for _, b := range bars {
		dedup[b.T] = b
	}
	if len(dedup) != 300 {
		t.Fatalf("不同时间戳根数 = %d, 期望 300", len(dedup))
	}

	first := dedup[1792021500]
	if first.O != 29932.75 || first.H != 29946.75 || first.L != 29885.75 || first.C != 29916.0 {
		t.Fatalf("首根 OHLC 不符: %+v", first)
	}
	if !first.HasVolume || first.V != 8858.0 {
		t.Fatalf("首根成交量不符: %+v", first)
	}

	last := dedup[1792114800]
	if last.O != 30067.25 || last.H != 30067.25 || last.L != 30056.0 || last.C != 30061.5 {
		t.Fatalf("末根 OHLC 不符: %+v", last)
	}
	if !last.HasVolume || last.V != 847.0 {
		t.Fatalf("末根成交量不符: %+v", last)
	}

	// 周期：298 个相邻间隔刚好 300s，剩下一个是 du 那根和整段历史之间的 3900s 空洞。
	gap300, other := 0, map[int64]int{}
	prev := int64(0)
	for _, b := range sortedTimes(dedup) {
		if prev != 0 {
			d := b - prev
			if d == 300 {
				gap300++
			} else {
				other[d]++
			}
		}
		prev = b
	}
	if gap300 != 298 {
		t.Fatalf("300s 相邻间隔数 = %d, 期望 298", gap300)
	}
	if len(other) != 1 || other[3900] != 1 {
		t.Fatalf("非 300s 间隔分布 = %v, 期望只有一处 3900s", other)
	}
}

func sortedTimes(m map[int64]Bar) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestMixedSymbolPageYieldsBarsOnlyFromChartSession 是这次「品种认错」的回归样本。
//
// 样本 testdata/mixed-symbols.ndjson 来自一次真实抓包（2026-10-16 13:00，
// 页面上开着 CME_MINI:MNQ1! 的 5 分钟图，同时自选/侧边栏在走别的品种的报价）。
// 里面包含：
//
//   - 一个会话里的 resolve_symbol / create_series / symbol_resolved（MNQ1!）
//   - 一条 300 根的 timescale_update 与三条 du 增量（都是那个会话的 sds_1）
//     —— 其中两条落在 NASDAQ:NDX 那条报价**之后**，正是旧版本会写错目录的那几根
//   - 一条**别的品种**的报价：{"m":"qsd","p":[...{"n":"NASDAQ:NDX",...,"pro_name":"NASDAQ:NDX"}]}
//
// 期望值是用独立脚本从原始抓包统计出来的（不跑被测代码）：全部 K 线来自
// cs_I5hYBRNGkSRt 的 sds_1，合并去重后 300 个时间戳；
// 而 NASDAQ:NDX 只在报价里出现，**一根 K 线都不该从它那儿抽出来**。
func TestMixedSymbolPageYieldsBarsOnlyFromChartSession(t *testing.T) {
	recs := readFixtureFile(t, "../../testdata/mixed-symbols.ndjson")

	barSources := map[string]int{}   // "方法|会话|序列" -> 根数
	quoteSymbols := map[string]int{} // 报价里出现的品种
	resolved := map[string]string{}
	for _, r := range recs {
		if r.Kind != "frame" {
			continue
		}
		for _, seg := range SplitFrame(r.Text) {
			if seg.Kind != KindJSON {
				continue
			}
			if scope, symKey, pro, ok := SymbolResolved(seg.Value); ok {
				resolved[scope+"/"+symKey] = pro
			}
			for _, n := range QuoteNames(seg.Value) {
				quoteSymbols[n]++
			}
			n := len(ExtractBars(seg.Value))
			if n == 0 {
				continue
			}
			key := seg.Method() + "|" + MessageScope(seg.Value) + "|" + "sds_1"
			barSources[key] += n
		}
	}

	if resolved["cs_I5hYBRNGkSRt/sds_sym_1"] != "CME_MINI:MNQ1!" {
		t.Fatalf("品种解析结果不符: %v", resolved)
	}
	// 整段样本里只有 300+3 根：tsu 300 根，三条 du 各 1 根（都是同一个时间戳的更新）
	if barSources["timescale_update|cs_I5hYBRNGkSRt|sds_1"] != 300 {
		t.Fatalf("timescale_update 的根数 = %d，期望 300：%v", barSources["timescale_update|cs_I5hYBRNGkSRt|sds_1"], barSources)
	}
	if barSources["du|cs_I5hYBRNGkSRt|sds_1"] != 3 {
		t.Fatalf("du 的根数 = %d，期望 3：%v", barSources["du|cs_I5hYBRNGkSRt|sds_1"], barSources)
	}
	if len(barSources) != 2 {
		t.Fatalf("K 线来源应当只有这两条，实际 %v", barSources)
	}
	// 报价里确实有别的品种 —— 而它们一根 K 线都没贡献
	if quoteSymbols["NASDAQ:NDX"] == 0 {
		t.Fatalf("样本里应当有 NASDAQ:NDX 的报价，实际 %v", quoteSymbols)
	}
}

func readFixtureFile(t *testing.T, rel string) []rawRecord {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("打不开样本 %s: %v", rel, err)
	}
	defer f.Close()
	var out []rawRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r rawRecord
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("样本第 %d 行不是合法 JSON: %v", len(out)+1, err)
		}
		out = append(out, r)
	}
	return out
}
