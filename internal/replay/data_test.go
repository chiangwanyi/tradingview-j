package replay

import (
	"math"
	"math/rand"
	"testing"
	"time"
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
	if inst.Bars[0].BarCount != 1 || inst.Bars[inst.Cursor].BarCount != inst.Cursor+1 {
		t.Fatalf("BarCount 不是从 1 开始按序递增")
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
