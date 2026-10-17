package replay

import (
	"math"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"tradingview-j/internal/store"
)

func TestCatalog(t *testing.T) {
	root := writeTestData(t, 3)
	ds := NewDataSource(root, time.Local)
	list, err := ds.Catalog()
	if err != nil {
		t.Fatalf("Catalog 失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应当有 1 个标的，实际 %d 个", len(list))
	}
	e := list[0]
	if e.Symbol != testSymbol || e.Folder != testFolder {
		t.Fatalf("标的解析错了：%+v", e)
	}
	if e.Days != 3 {
		t.Fatalf("天数应当是 3，实际 %d", e.Days)
	}
	if e.FirstDate != "2026-01-05" || e.LastDate != "2026-01-07" {
		t.Fatalf("日期范围错了：%s ~ %s", e.FirstDate, e.LastDate)
	}
}

func TestOpenWithExplicitAnchor(t *testing.T) {
	root := writeTestData(t, 5)
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-06 10:00", 30, 1, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	if inst.Anchor != "2026-01-06 10:00" {
		t.Fatalf("锚点应当是 2026-01-06 10:00，实际 %s", inst.Anchor)
	}
	if got := inst.Bars[inst.Cursor].Time; got != inst.Anchor {
		t.Fatalf("游标没有停在锚点上：%s", got)
	}
	// 历史 30 根：01-05 有 25 根，01-06 到 10:00 有 13 根 -> 38 根足够 30
	if inst.Cursor < 29 {
		t.Fatalf("锚点前应当至少有 30 根，实际游标 %d", inst.Cursor)
	}
	for i := 1; i <= inst.Cursor; i++ {
		if inst.Bars[i-1].Time >= inst.Bars[i].Time {
			t.Fatalf("K 线没有按时间升序：%s >= %s", inst.Bars[i-1].Time, inst.Bars[i].Time)
		}
	}
	// 价格一律只有两位小数（CSV 里的浮点噪声在加载时就被抹平了）
	for _, b := range inst.Bars {
		if math.Abs(b.Close*100-math.Round(b.Close*100)) > 1e-9 {
			t.Fatalf("%s 的收盘价没有修到两位小数：%v", b.Time, b.Close)
		}
	}
	if inst.Tick <= 0 {
		t.Fatalf("实例应当带上最小跳动（tick），实际 %v", inst.Tick)
	}
	if inst.hiddenBars() == 0 {
		t.Fatalf("向后应当预加载了隐藏 K 线")
	}
}

func TestOpenRejectsUnknownTimeAndSymbol(t *testing.T) {
	root := writeTestData(t, 3)
	ds := NewDataSource(root, time.Local)
	if _, err := ds.Open(testSymbol, "2026-01-06 10:03", 30, 1, nil); err == nil {
		t.Fatal("不在 CSV 里的时间点应当报错")
	}
	if _, err := ds.Open(testSymbol, "2025-12-31 10:00", 30, 1, nil); err == nil {
		t.Fatal("没数据的日期应当报错")
	}
	if _, err := ds.Open("NOT:THERE", "", 30, 1, nil); err == nil {
		t.Fatal("不存在的标的应当报错")
	}
}

func TestRandomAnchorIsReproducible(t *testing.T) {
	root := writeTestData(t, 6)
	ds := NewDataSource(root, time.Local)

	one, err := ds.Open(testSymbol, "", 40, 2, rand.New(rand.NewSource(42)))
	if err != nil {
		t.Fatalf("随机打开失败：%v", err)
	}
	two, err := ds.Open(testSymbol, "", 40, 2, rand.New(rand.NewSource(42)))
	if err != nil {
		t.Fatalf("随机打开失败：%v", err)
	}
	if one.Anchor != two.Anchor {
		t.Fatalf("同一个种子应当落到同一个锚点：%s vs %s", one.Anchor, two.Anchor)
	}
	if one.Anchor == "" || one.Cursor == 0 {
		t.Fatalf("随机锚点不应当停在第一根：cursor=%d anchor=%s", one.Cursor, one.Anchor)
	}
}

func TestEMA20(t *testing.T) {
	bars := make([]Bar, 25)
	for i := range bars {
		bars[i] = Bar{Close: float64(i + 1)}
	}
	computeEMA(bars)
	if bars[18].Ema20 != nil {
		t.Fatal("第 19 根之前不该有 EMA20")
	}
	if bars[19].Ema20 == nil {
		t.Fatal("第 20 根应当有 EMA20 种子值")
	}
	// 前 20 根的简单平均 = (1+...+20)/20 = 10.5
	if v := *bars[19].Ema20; math.Abs(v-10.5) > 1e-9 {
		t.Fatalf("EMA20 种子应当是 10.5，实际 %v", v)
	}
	// 第 21 根：10.5 + (21-10.5)*2/21 = 11.5
	want := 10.5 + (21-10.5)*2.0/21.0
	if v := *bars[20].Ema20; math.Abs(v-want) > 1e-9 {
		t.Fatalf("EMA20 递推错了：想要 %v，实际 %v", want, v)
	}
}

func TestStepPastPreloadedWindowLoadsNextDay(t *testing.T) {
	root := writeTestData(t, 5)
	ds := NewDataSource(root, time.Local)
	// 锚点选在第一天最后一根：预加载 1 天 -> 已加载到第二天，再步进就要续读第三天
	inst, err := ds.Open(testSymbol, "2026-01-05 11:00", 10, 1, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	if inst.hasMore() == false {
		t.Fatal("后面还有日文件，hasMore 应当是 true")
	}
	// 走完所有预加载的根，再走一根，应当触发续读
	for i := 0; i < 60; i++ {
		if _, _, err := inst.advance(ds); err != nil {
			t.Fatalf("第 %d 步失败：%v", i, err)
		}
	}
	if inst.Bars[inst.Cursor].Time <= "2026-01-06 23:59" {
		t.Fatalf("没有续读到后面的日文件，当前 %s", inst.Bars[inst.Cursor].Time)
	}
}

func TestStepStopsAtEndOfData(t *testing.T) {
	root := writeTestData(t, 2)
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-06 11:00", 20, 1, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	for {
		if _, _, err := inst.advance(ds); err != nil {
			if err != ErrNoMoreBars {
				t.Fatalf("末尾应当返回 ErrNoMoreBars，实际 %v", err)
			}
			break
		}
	}
	if inst.Cursor != len(inst.Bars)-1 {
		t.Fatalf("游标应当停在最后一根")
	}
}

func TestSnapshotOnlyExposesRevealedBars(t *testing.T) {
	root := writeTestData(t, 4)
	inst := openTestInstance(t, root, "2026-01-06 10:00", 30, 2)
	snap := inst.snapshot(nil)
	if len(snap.Bars) != inst.Cursor+1 {
		t.Fatalf("快照应当只含已揭示的 %d 根，实际 %d", inst.Cursor+1, len(snap.Bars))
	}
	if snap.Bars[len(snap.Bars)-1].Time != inst.Anchor {
		t.Fatalf("快照最后一根应当是锚点")
	}
	if snap.HiddenBars != inst.hiddenBars() || snap.HiddenBars == 0 {
		t.Fatalf("隐藏根数不对：%d", snap.HiddenBars)
	}
}

func TestDedupeBarsKeepsLater(t *testing.T) {
	bars := []Bar{
		{Time: "2026-01-05 09:00", Close: 1},
		{Time: "2026-01-05 09:05", Close: 2},
		{Time: "2026-01-05 09:05", Close: 3},
	}
	out := dedupeBars(bars)
	if len(out) != 2 || out[1].Close != 3 {
		t.Fatalf("去重应当保留后一份：%+v", out)
	}
}

// ---------------------------------------------------------------------------
// 「下一根」必须是紧邻的那一根（0.5.1 修的 bug）
// ---------------------------------------------------------------------------
//
// 症状：随机加载或指定时间加载之后点【下一根 →】，拿到的不是锚点之后 5 分钟那根，
// 而是**第二天 00:00** 那根 —— 锚点当天剩下的几十上百根被整个跳过。
//
// 根因在 Open：锚点日只放进了 rows[:rowIdx+1]（到锚点为止），随后直接从 dayIdx+1
// 开始预加载，于是 Bars[Cursor+1] 就是第二天日文件的第一行。真实数据每天是
// 00:00~23:55 连着的，所以「第二天的 00:00」这一跳看着像正常的跨日，实际是丢了一整段。
//
// 下面几条分别钉住：指定时间、随机时间、锚点正好是当天最后一根（这一跳跨日是**对的**），
// 以及「加载进来的 Bars 是日文件顺序拼接之后的连续片段」这个更强的性质。

// expectedNextBar 按日文件算出「锚点之后真正紧邻的那一根」：
// 同一天还有就取同一天的下一行，没有才取下一个日文件的第一行。
// ok=false 表示数据在锚点这里就到头了。
//
// 刻意从 CSV 里重新读一遍，而不是拿实例自己的 Bars 去对照 —— 否则 bug 会被自己掩盖。
func expectedNextBar(t *testing.T, root, folder, anchor string) (string, bool) {
	t.Helper()
	dir := filepath.Join(root, folder)
	files, err := store.DayFiles(dir)
	if err != nil {
		t.Fatalf("读日文件列表失败：%v", err)
	}
	dayIdx := indexOfFile(files, anchor[:10]+".csv")
	if dayIdx < 0 {
		t.Fatalf("锚点 %s 那天没有日文件", anchor)
	}
	rows, _, err := store.ReadDayRows(filepath.Join(dir, files[dayIdx]), time.Local)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", files[dayIdx], err)
	}
	for i := range rows {
		if rows[i].Time != anchor {
			continue
		}
		if i+1 < len(rows) {
			return rows[i+1].Time, true
		}
		for d := dayIdx + 1; d < len(files); d++ {
			next, _, err := store.ReadDayRows(filepath.Join(dir, files[d]), time.Local)
			if err != nil {
				t.Fatalf("读 %s 失败：%v", files[d], err)
			}
			if len(next) > 0 {
				return next[0].Time, true
			}
		}
		return "", false
	}
	t.Fatalf("锚点 %s 不在 %s 里", anchor, files[dayIdx])
	return "", false
}

func TestStepAfterExplicitAnchorReturnsSameDayNextBar(t *testing.T) {
	root := writeTestData(t, 4) // 每天 09:00 ~ 11:00，25 根
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-06 10:00", 30, 1, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	// 锚点当天还有 10:05 10:10 10:15 …，前三步必须落在这里，不能跨到 01-07
	want := []string{"2026-01-06 10:05", "2026-01-06 10:10", "2026-01-06 10:15"}
	for i, w := range want {
		bar, _, err := inst.advance(ds)
		if err != nil {
			t.Fatalf("第 %d 步失败：%v", i+1, err)
		}
		if bar.Time != w {
			t.Fatalf("第 %d 步应当是锚点当天的 %s，实际是 %s", i+1, w, bar.Time)
		}
	}
	if inst.Bars[inst.Cursor].Time != "2026-01-06 10:15" {
		t.Fatalf("游标没有跟上步进：%s", inst.Bars[inst.Cursor].Time)
	}
}

func TestStepAfterRandomAnchorReturnsSameDayNextBar(t *testing.T) {
	root := writeTestData(t, 6)
	ds := NewDataSource(root, time.Local)
	for seed := int64(1); seed <= 30; seed++ {
		inst, err := ds.Open(testSymbol, "", 40, 2, rand.New(rand.NewSource(seed)))
		if err != nil {
			t.Fatalf("种子 %d 随机打开失败：%v", seed, err)
		}
		want, ok := expectedNextBar(t, root, inst.Folder, inst.Anchor)
		if !ok {
			// 随机锚点避开最后一天，所以后面必然还有数据
			t.Fatalf("种子 %d：锚点 %s 之后应当还有 K 线", seed, inst.Anchor)
		}
		bar, _, err := inst.advance(ds)
		if err != nil {
			t.Fatalf("种子 %d 步进失败：%v", seed, err)
		}
		if bar.Time != want {
			t.Fatalf("种子 %d：锚点 %s 的下一根应当是 %s，实际是 %s", seed, inst.Anchor, want, bar.Time)
		}
	}
}

// 锚点正好是当天最后一根时，下一根**就该**跨到第二天第一根 —— 这条钉住「别修过头」。
func TestStepAcrossDayBoundaryWhenAnchorIsLastBar(t *testing.T) {
	root := writeTestData(t, 3)
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-05 11:00", 20, 1, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	bar, _, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if bar.Time != "2026-01-06 09:00" {
		t.Fatalf("锚点是当天最后一根时，下一根应当是 2026-01-06 09:00，实际是 %s", bar.Time)
	}
}

// 更强的性质：不管锚点落在哪儿，加载进来的 Bars 必须是「所有日文件按顺序拼接」之后的一段
// **连续**切片 —— 中间不许缺、不许乱序、不许夹带别的日子。丢数据这类 bug 都在这里现形。
func TestLoadedBarsAreContiguousSliceOfDayFiles(t *testing.T) {
	root := writeTestData(t, 5)
	ds := NewDataSource(root, time.Local)

	dir := filepath.Join(root, testFolder)
	files, err := store.DayFiles(dir)
	if err != nil {
		t.Fatalf("读日文件列表失败：%v", err)
	}
	var all []string
	for _, f := range files {
		rows, _, err := store.ReadDayRows(filepath.Join(dir, f), time.Local)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", f, err)
		}
		for _, r := range rows {
			all = append(all, r.Time)
		}
	}

	for _, anchor := range []string{"2026-01-05 09:00", "2026-01-06 09:30", "2026-01-07 11:00", "2026-01-08 10:15"} {
		inst, err := ds.Open(testSymbol, anchor, 60, 2, nil)
		if err != nil {
			t.Fatalf("锚点 %s 打开失败：%v", anchor, err)
		}
		start := -1
		for i, s := range all {
			if s == inst.Bars[0].Time {
				start = i
				break
			}
		}
		if start < 0 {
			t.Fatalf("锚点 %s：第一根 %s 不在日文件拼接结果里", anchor, inst.Bars[0].Time)
		}
		if start+len(inst.Bars) > len(all) {
			t.Fatalf("锚点 %s：加载了 %d 根，但后面只剩 %d 根", anchor, len(inst.Bars), len(all)-start)
		}
		for i, b := range inst.Bars {
			if all[start+i] != b.Time {
				t.Fatalf("锚点 %s：Bars 不是连续片段，第 %d 根是 %s，应当是 %s", anchor, i, b.Time, all[start+i])
			}
		}
		// 锚点本身不能丢：快照最后一根就是它，游标停在这里
		if inst.Bars[inst.Cursor].Time != anchor {
			t.Fatalf("锚点 %s：游标停在 %s", anchor, inst.Bars[inst.Cursor].Time)
		}
	}
}
