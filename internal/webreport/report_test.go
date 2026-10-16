package webreport

// Build 是纯函数（只读入参、不碰磁盘、不看时钟），所以这一层不用先造数据目录，
// 拿一份手工构造的 store.ScanResult 就能断言。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// sample 造一份「一个标的、一天、276 根但按首尾推算应有 288 根」的扫描结果。
// 数字和 analyze.go 的报表测试用的是同一套（276 根 / 缺口 12 / 断档 1时5分）。
func sample(t *testing.T) *store.ScanResult {
	t.Helper()
	first := mustT(t, "2026-10-16 00:00")
	last := mustT(t, "2026-10-16 23:55")
	d := store.DayStat{
		Date: "2026-10-16", Path: "x/2026-10-16.csv",
		Bars: 276, Rows: 276, FirstT: first, LastT: last,
		Span: 288, Missing: 12, MaxGap: 3900, Bytes: 15037,
	}
	f := store.FolderStat{
		Folder: "CME_MINI_MES1!", Symbol: "CME_MINI:MES1!", Known: true,
		Days: []store.DayStat{d}, Bars: d.Bars, Bytes: d.Bytes,
		FirstT: first, LastT: last, HasTime: true,
	}
	return &store.ScanResult{
		Root: "D:/data", Step: 300, Exists: true,
		Folders: []store.FolderStat{f},
		Days:    len(f.Days), Bars: f.Bars, Bytes: f.Bytes,
	}
}

func TestBuildKeepsStoreNumbers(t *testing.T) {
	res := sample(t)
	rep := Build(res, cst, "1.2.0")

	if rep.Meta.Root != "D:/data" || rep.Meta.Step != 300 || rep.Meta.Version != "1.2.0" {
		t.Fatalf("meta 不对：%+v", rep.Meta)
	}
	if !strings.HasPrefix(rep.Meta.TZ, "UTC+8") {
		t.Fatalf("时区没带上：%q（网页上要显示它，换时区缺口数会变）", rep.Meta.TZ)
	}
	// 光写 "Local" 等于什么都没说，实际偏移要一起给出来
	if !strings.Contains(rep.Meta.TZ, "UTC+08:00") {
		t.Fatalf("时区里应当带上实际偏移：%q", rep.Meta.TZ)
	}
	if !rep.Meta.Exists {
		t.Fatal("Exists 应当透传")
	}

	want := Totals{Symbols: 1, Days: 1, Bars: 276, Bytes: 15037}
	if rep.Totals != want {
		t.Fatalf("合计 = %+v，期望 %+v", rep.Totals, want)
	}

	if rep.Span == nil {
		t.Fatal("有时间信息却没有 span")
	}
	if rep.Span.FirstT != mustT(t, "2026-10-16 00:00") || rep.Span.LastT != mustT(t, "2026-10-16 23:55") {
		t.Fatalf("span = %+v", rep.Span)
	}

	if len(rep.Folders) != 1 {
		t.Fatalf("标的数 = %d", len(rep.Folders))
	}
	fo := rep.Folders[0]
	if fo.Symbol != "CME_MINI:MES1!" || fo.Folder != "CME_MINI_MES1!" || !fo.Known {
		t.Fatalf("标的映射不对：%+v", fo)
	}
	if len(fo.Files) != 1 {
		t.Fatalf("日文件数 = %d", len(fo.Files))
	}
	d := fo.Files[0]
	if d.Bars != 276 || d.Rows != 276 || d.Missing != 12 || d.Span != 288 || d.MaxGap != 3900 {
		t.Fatalf("日文件数字被改过：%+v", d)
	}
	if d.Bytes != 15037 {
		t.Fatalf("文件大小 = %d", d.Bytes)
	}

	if len(rep.ByDate) != 1 || rep.ByDate[0].Date != "2026-10-16" || rep.ByDate[0].Bars != 276 {
		t.Fatalf("按日期合计不对：%+v", rep.ByDate)
	}
}

func TestBuildEmptyResultMarshalsToEmptyArrays(t *testing.T) {
	// 前端直接 folders.length / notes.length 判断，null 会让 Vue 模板里的 .length 报错。
	// 所以空的必须是 []，不是 null。
	rep := Build(&store.ScanResult{Root: "D:/data", Step: 300}, cst, "1.2.0")
	if rep.Span != nil {
		t.Fatal("没有任何标的时不该有 span")
	}

	buf, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf)
	for _, want := range []string{`"folders":[]`, `"byDate":[]`, `"notes":[]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("JSON 里缺少 %s：\n%s", want, got)
		}
	}
	if strings.Contains(got, "null") {
		t.Fatalf("JSON 里出现了 null：\n%s", got)
	}
}

func TestBuildPassesNotesAndErrorsThrough(t *testing.T) {
	res := sample(t)
	res.Notes = []string{"读不了数据目录：拒绝访问"}
	res.Folders[0].Days = append(res.Folders[0].Days, store.DayStat{
		Date: "2026-10-17", Err: "读不了：拒绝访问",
	})

	rep := Build(res, cst, "1.2.0")
	if len(rep.Notes) != 1 || rep.Notes[0] != "读不了数据目录：拒绝访问" {
		t.Fatalf("notes 没透传：%+v", rep.Notes)
	}
	if got := rep.Folders[0].Files[1].Err; got != "读不了：拒绝访问" {
		t.Fatalf("日文件错误没透传：%q", got)
	}
}

func TestBuildSurvivesMissingLocation(t *testing.T) {
	// loc 传 nil 不该 panic，退回本机时区
	rep := Build(sample(t), nil, "1.2.0")
	if rep.Meta.TZ == "" {
		t.Fatal("没退回本机时区")
	}
}
