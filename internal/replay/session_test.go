package replay

// 「每日开盘首根」这条判据的测试。数据按北京时间存，日盘随美国夏令时在 6 点 / 7 点开，
// 所以这里的断言全部用真实数据里出现过的形状：23:55 -> 00:00 的日历日接缝**不是**开盘，
// 04:55 -> 06:00 / 05:55 -> 07:00 的停盘缺口才是。

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsSessionOpenUsesGapNotMidnight(t *testing.T) {
	cases := []struct {
		prev, cur string
		want      bool
		why       string
	}{
		{"2026-01-05 23:55", "2026-01-06 00:00", false, "跨日历日但只隔 5 分钟：是同一个交易日的延续"},
		{"2026-01-06 04:55", "2026-01-06 06:00", true, "夏令时：盘间断档 65 分钟"},
		{"2026-01-06 05:55", "2026-01-06 07:00", true, "冬令时：开盘晚一小时"},
		{"2026-01-06 23:55", "2026-01-07 00:00", false, "又一次日历日接缝"},
		{"2026-01-06 10:00", "2026-01-06 10:20", false, "数据里的 20 分钟空洞不算开盘"},
		{"", "2026-01-06 07:00", false, "前面没有根可比时由调用方兜底，这里不算缺口"},
	}
	for _, c := range cases {
		if got := isSessionOpen(c.prev, c.cur); got != c.want {
			t.Fatalf("isSessionOpen(%q, %q) = %v，应当是 %v（%s）", c.prev, c.cur, got, c.want, c.why)
		}
	}
}

func TestSessionOpenIdxInOneDayFile(t *testing.T) {
	// 一整天的日文件：00:00..04:55 是上一个交易日的尾巴（和前一天 23:55 只隔 5 分钟），
	// 04:55 -> 06:00 是盘间断档，06:00 才是开盘。
	var times []string
	for m := 0; m <= 4*60+55; m += 5 {
		times = append(times, fmt.Sprintf("2026-01-06 %02d:%02d", m/60, m%60))
	}
	for m := 6 * 60; m < 8*60; m += 5 {
		times = append(times, fmt.Sprintf("2026-01-06 %02d:%02d", m/60, m%60))
	}
	if got := sessionOpenIdx(times, "2026-01-05 23:55"); got < 0 || times[got] != "2026-01-06 06:00" {
		t.Fatalf("开盘首根应当是 06:00，实际下标 %d", got)
	}
}

// writeSessionData 造一份「只有接缝」的数据：files 是 日期 -> 该日文件里的时刻（HH:MM）。
func writeSessionData(t *testing.T, files map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, testFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	idx := map[string]any{"version": 1, "symbols": map[string]any{
		testSymbol: map[string]any{"folder": testFolder, "tick": 0.25, "tickValue": 0.5},
	}}
	buf, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "_symbols.json"), buf, 0o644); err != nil {
		t.Fatalf("写索引失败：%v", err)
	}
	for date, hhmm := range files {
		f, err := os.Create(filepath.Join(dir, date+".csv"))
		if err != nil {
			t.Fatalf("建 CSV 失败：%v", err)
		}
		fmt.Fprintln(f, "time,open,high,low,close,volume")
		for i, hm := range hhmm {
			open := 100 + float64(i)*0.5
			fmt.Fprintf(f, "%s %s,%v,%v,%v,%v,%d\n", date, hm, num(open), num(open+1), num(open-1), num(open+0.25), 100+i)
		}
		f.Close()
	}
	return root
}

func TestOpenSessionSkipsTailOnlyDay(t *testing.T) {
	// 最后一个日文件只是上一个交易日跨过午夜剩下的尾巴（周六文件）：那天没有开盘，
	// 随机挑到它就得换一天，落在前一天的开盘首根上。
	root := writeSessionData(t, map[string][]string{
		"2026-01-08": {"06:00", "06:05", "23:55"},
		"2026-01-09": {"00:00", "00:05", "00:10"},
	})
	for seed := int64(1); seed <= 5; seed++ {
		inst, err := NewDataSource(root, time.Local).OpenSession(testSymbol, 40, 1, rand.New(rand.NewSource(seed)))
		if err != nil {
			t.Fatalf("OpenSession 失败：%v", err)
		}
		if inst.Anchor != "2026-01-08 06:00" {
			t.Fatalf("锚点应当是 2026-01-08 06:00（种子 %d），实际 %s", seed, inst.Anchor)
		}
		if got := inst.Bars[inst.Cursor].Time; got != inst.Anchor {
			t.Fatalf("游标没有停在锚点上：%s", got)
		}
	}
}

func TestOpenSessionAtFileBoundary(t *testing.T) {
	// 缺口正好落在文件边界上（周一/假期后：日文件直接从开盘那根开始），
	// 第一根也必须是开盘根 —— 判据要看上一份数据的最后一根。
	root := writeSessionData(t, map[string][]string{
		"2026-01-08": {"23:55"},
		"2026-01-09": {"06:00", "06:05", "06:10"},
		"2026-01-10": {"06:00", "06:05", "06:10"},
	})
	inst, err := NewDataSource(root, time.Local).OpenSession(testSymbol, 40, 1, nil)
	if err != nil {
		t.Fatalf("OpenSession 失败：%v", err)
	}
	if inst.Anchor != "2026-01-09 06:00" { // 01-10 是尾日，不参选；01-09 是唯一候选
		t.Fatalf("锚点应当是 2026-01-09 06:00，实际 %s", inst.Anchor)
	}
}

// 【新增实例（开盘）】挑的是**随机一天**的开盘：每次点都该可能不一样，
// 但锚点必须是那天的第 1 根，且首尾两天不参选（首日没历史、尾日之后没行情）。
func TestOpenSessionPicksRandomDayOpen(t *testing.T) {
	root := writeTestData(t, 6) // 01-05 ~ 01-10，每天 09:00 起 25 根
	ds := NewDataSource(root, time.Local)
	seen := map[string]bool{}
	for seed := int64(1); seed <= 20; seed++ {
		inst, err := ds.OpenSession(testSymbol, 40, 1, rand.New(rand.NewSource(seed)))
		if err != nil {
			t.Fatalf("OpenSession 失败（种子 %d）：%v", seed, err)
		}
		day, clock := inst.Anchor[:10], inst.Anchor[11:]
		if clock != "09:00" {
			t.Fatalf("锚点必须是当天第 1 根（09:00），实际 %s", inst.Anchor)
		}
		if day == "2026-01-05" || day == "2026-01-10" {
			t.Fatalf("首尾两天不该参选，实际选中 %s", inst.Anchor)
		}
		seen[inst.Anchor] = true
	}
	if len(seen) < 2 {
		t.Fatalf("20 次都落在同一天（%v），随机没有生效", seen)
	}
}

func TestJumpToNextOpen(t *testing.T) {
	root := writeTestData(t, 4) // 每天 09:00 起 25 根
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-06 10:00", 40, 1, nil)
	if err != nil {
		t.Fatalf("打开实例失败：%v", err)
	}
	bar, _, err := inst.jumpToNextOpen(ds)
	if err != nil {
		t.Fatalf("跳转失败：%v", err)
	}
	if bar.Time != "2026-01-07 09:00" {
		t.Fatalf("应当停在 2026-01-07 09:00，实际 %s", bar.Time)
	}
	// 中间那些根是逐根走过去的（不是直接挪游标）：前一根必须是上一天的最后一根
	if prev := inst.Bars[inst.Cursor-1].Time; prev != "2026-01-06 11:00" {
		t.Fatalf("开盘前一根应当是 2026-01-06 11:00，实际 %s", prev)
	}
	// 再跳一次：不许停在原地，也不许越过下一天
	bar, _, err = inst.jumpToNextOpen(ds)
	if err != nil {
		t.Fatalf("第二次跳转失败：%v", err)
	}
	if bar.Time != "2026-01-08 09:00" {
		t.Fatalf("第二次应当停在 2026-01-08 09:00，实际 %s", bar.Time)
	}
}

func TestJumpToNextOpenWithoutNextDay(t *testing.T) {
	root := writeTestData(t, 2)
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, "2026-01-06 10:00", 40, 1, nil)
	if err != nil {
		t.Fatalf("打开实例失败：%v", err)
	}
	if _, _, err := inst.jumpToNextOpen(ds); err == nil {
		t.Fatal("数据里没有下一天时必须报错，不能悄悄停在原地")
	}
}
