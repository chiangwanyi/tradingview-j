package termreport

// 报表排版的回归测试。
//
// 存在的理由：表头是中文（"日期"、"最大断档"）、数据行几乎全是 ASCII。
// 一旦有人图省事把「按显示宽度补齐」换成 fmt 的 %-12s 或 len()，
// 中文列会当场少算一半宽度、整张表往左串行 —— 而那种歪斜在代码里看不出来，
// 只有盯着终端才发现。这里用「每行显示宽度必须相等」把口径钉死。

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tradingview-j/internal/logx"
	"tradingview-j/internal/store"
)

var cst = time.FixedZone("UTC+8", 8*3600)

func mustT(t *testing.T, stamp string) int64 {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", stamp, cst)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Unix()
}

// 两天的所有字段都做成同样的宽度，这样任何一行比表头宽/窄都只能是排版算错了。
func day(t *testing.T, date string) store.DayStat {
	t.Helper()
	first := mustT(t, date+" 00:00")
	last := mustT(t, date+" 23:55")
	return store.DayStat{
		Date: date, Bars: 276, Span: 288, Missing: 12, MaxGap: 3900, // 1时5分
		FirstT: first, LastT: last, Bytes: 15037, // 14.7 KB
	}
}

func TestFolderTableColumnsAlign(t *testing.T) {
	var buf bytes.Buffer
	lg := logx.NewTo(&buf)

	f := store.FolderStat{
		Folder: "CME_MINI_MNQ1!", Symbol: "CME_MINI:MNQ1!", Known: true,
		FirstT: mustT(t, "2026-10-16 00:00"), LastT: mustT(t, "2026-10-17 23:55"), HasTime: true,
	}
	f.Days = []store.DayStat{day(t, "2026-10-16"), day(t, "2026-10-17")}
	// 合计是 scanFolder 算出来的，这里手工填（printFolder 只负责排版）
	for _, d := range f.Days {
		f.Bars += d.Bars
		f.Bytes += d.Bytes
	}
	printFolder(lg, f, cst)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 5 { // 标题 / 统计 / 表头 / 两行数据
		t.Fatalf("行数 = %d：\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "CME_MINI:MNQ1!") || !strings.Contains(lines[0], "CME_MINI_MNQ1!") {
		t.Fatalf("标题没同时给出标的与文件夹：%q", lines[0])
	}
	if !strings.Contains(lines[1], "2 天") || !strings.Contains(lines[1], "552 根") {
		t.Fatalf("统计行 = %q", lines[1])
	}

	// 表头（含中文）与两行数据（ASCII）必须占同样的列宽
	want := logx.Width(lines[2])
	for i := 3; i < 5; i++ {
		if got := logx.Width(lines[i]); got != want {
			t.Fatalf("第 %d 行宽度 %d，表头是 %d，表格串行了：\n%s\n%s", i, got, want, lines[2], lines[i])
		}
	}
	// 顺带确认那个中文单位的断档没有被漏掉
	if !strings.Contains(lines[3], "1时5分") {
		t.Fatalf("断档没按人话格式化：%q", lines[3])
	}
}

func TestByDateTableColumnsAlign(t *testing.T) {
	var buf bytes.Buffer
	lg := logx.NewTo(&buf)

	res := &store.ScanResult{
		Step: 300,
		Folders: []store.FolderStat{
			{Folder: "A", Symbol: "A", Days: []store.DayStat{
				{Date: "2026-10-16", Bars: 276, FirstT: 1, LastT: 2, Bytes: 100},
				{Date: "2026-10-17", Bars: 60, FirstT: 1, LastT: 2, Bytes: 100},
			}},
			{Folder: "B", Symbol: "B", Days: []store.DayStat{
				{Date: "2026-10-16", Bars: 3000, FirstT: 1, LastT: 2, Bytes: 100},
			}},
		},
	}
	printByDate(lg, res)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 { // 小标题 / 表头 / 两行
		t.Fatalf("行数 = %d：\n%s", len(lines), buf.String())
	}
	want := logx.Width(lines[1])
	for i := 2; i < 4; i++ {
		if got := logx.Width(lines[i]); got != want {
			t.Fatalf("第 %d 行宽度 %d，表头是 %d：\n%s\n%s", i, got, want, lines[1], lines[i])
		}
	}
	// 10-16 两个标的合计 3,276 根
	if !strings.Contains(lines[2], "3,276") || !strings.Contains(lines[2], "2") {
		t.Fatalf("按日期合计不对：%q", lines[2])
	}
}

func TestHumanBytesAndComma(t *testing.T) {
	if got := humanBytes(15037); got != "14.7 KB" {
		t.Fatalf("humanBytes(15037) = %q", got)
	}
	if got := humanBytes(573 * 1024); got != "573.0 KB" {
		t.Fatalf("humanBytes = %q", got)
	}
	if got := humanBytes(5 * 1024 * 1024); got != "5.0 MB" {
		t.Fatalf("humanBytes = %q", got)
	}
	if got := humanBytes(512); got != "512 B" {
		t.Fatalf("humanBytes = %q", got)
	}
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1,000"}, {10822, "10,822"}, {1234567, "1,234,567"}} {
		if got := comma(c.in); got != c.want {
			t.Fatalf("comma(%d) = %q, 期望 %q", c.in, got, c.want)
		}
	}
	if got := gapText(300); got != "5分" {
		t.Fatalf("gapText(300) = %q", got)
	}
	if got := gapText(3900); got != "1时5分" {
		t.Fatalf("gapText(3900) = %q", got)
	}
	if got := gapText(7200); got != "2时" {
		t.Fatalf("gapText(7200) = %q", got)
	}
}
