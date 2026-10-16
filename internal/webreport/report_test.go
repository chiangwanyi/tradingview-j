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

// sample 造一份「一个标的、一个周五、276 根」的扫描结果 —— 这是这份数据里最普通的一天：
// 00:00~23:55 一共 288 个位置，中间被每日结算休市挖掉 12 根（05:00~05:55），剩 276 根。
func sample(t *testing.T) *store.ScanResult {
	t.Helper()
	first := mustT(t, "2026-10-16 00:00")
	last := mustT(t, "2026-10-16 23:55")
	d := store.DayStat{
		Date: "2026-10-16", Path: "x/2026-10-16.csv",
		Bars: 276, Rows: 276, FirstT: first, LastT: last,
		Span: 288, Missing: 12, MaxGap: 3900, Bytes: 15037,
		Gaps: []store.Gap{{
			StartT: mustT(t, "2026-10-16 05:00"),
			EndT:   mustT(t, "2026-10-16 05:55"),
			Bars:   12,
		}},
		Expect: []int{276},
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
	if d.Bars != 276 || d.Rows != 276 {
		t.Fatalf("日文件数字被改过：%+v", d)
	}
	if d.Bytes != 15037 {
		t.Fatalf("文件大小 = %d", d.Bytes)
	}

	if len(rep.ByDate) != 1 || rep.ByDate[0].Date != "2026-10-16" || rep.ByDate[0].Bars != 276 {
		t.Fatalf("按日期合计不对：%+v", rep.ByDate)
	}
}

// 「缺在哪一刻到哪一刻」是这一版报告的重点：缺口/最大断档那种汇总数字被整个去掉了，
// 换成一段段列出来的断档区间。同时把「这一天该有多少根」的判定也钉住。
func TestBuildCarriesGapsAndVerdicts(t *testing.T) {
	res := sample(t)
	res.Folders[0].Days[0].Mismatch = true

	rep := Build(res, cst, "1.3.0")
	d := rep.Folders[0].Files[0]

	if len(d.Gaps) != 1 {
		t.Fatalf("断档没透传：%+v", d.Gaps)
	}
	g := d.Gaps[0]
	if g.StartT != mustT(t, "2026-10-16 05:00") || g.EndT != mustT(t, "2026-10-16 05:55") || g.Bars != 12 {
		t.Fatalf("断档的起止/根数不对：%+v", g)
	}
	if len(d.Expect) != 1 || d.Expect[0] != 276 {
		t.Fatalf("应有根数没透传：%+v（这条要显示在页面上，前端不许自己填）", d.Expect)
	}
	if d.BarsOff {
		t.Fatalf("周五 276 根正是应有的值，不该判成不符：%+v", d)
	}
	if d.Anomaly {
		t.Fatalf("干净的数据不该是异常：%+v", d)
	}
	if !d.Mismatch {
		t.Fatal("mismatch 没透传 —— 日历要标黄的就是它")
	}
}

func TestBuildFlagsBarsOffAsAnomaly(t *testing.T) {
	res := sample(t)

	// 判定只在 store 里做，Build 一次都不许重算：这里把根数改成 275、而 BarsOff 仍是
	// scanner 给的 false，透传出来就必须还是 false。哪天有人在 Build（或前端）里
	// 自己按 276 比一遍，这条会先红。
	res.Folders[0].Days[0].Bars = 275
	if d := Build(res, cst, "1.3.0").Folders[0].Files[0]; d.BarsOff || d.Anomaly {
		t.Fatalf("Build 不该自己重算根数判定（那是 store 的事）：%+v", d)
	}

	// 反向：scanner 说不符，就得原样变成 barsOff + anomaly 两个字段
	res.Folders[0].Days[0].BarsOff = true
	rep := Build(res, cst, "1.3.0")
	d := rep.Folders[0].Files[0]
	if !d.BarsOff || !d.Anomaly {
		t.Fatalf("根数不符应当既是 barsOff 也是 anomaly：%+v", d)
	}

	// 判定表也要原样送到前端 —— 页面上那张「星期几应当多少根」就是用它的。
	// 顺序按中文习惯从周一开始（内部那张表是按 time.Weekday 索引的，周日是 0）。
	if len(rep.Meta.ExpectRule) != 7 || rep.Meta.ExpectRule[0].Weekday != "周一" {
		t.Fatalf("判定表顺序不对：%+v", rep.Meta.ExpectRule)
	}
	mon := rep.Meta.ExpectRule[0]
	if len(mon.Bars) != 2 || mon.Bars[0] != 216 || mon.Bars[1] != 204 {
		t.Fatalf("周一的应有根数应当是 216/204（夏令时/冬令时）：%+v", mon)
	}
}

func TestBuildCarriesByDateIssueCount(t *testing.T) {
	// 日历上「这一天有异常」的红标用的是按日期合计里的 issues —— 它是 Go 算的，
	// 前端不许自己扫一遍文件去凑这个数（否则又是一份口径）。
	res := sample(t)
	res.Folders[0].Days[0].Skipped = 3
	res.BarsOff, res.MismatchDays = 1, 1

	rep := Build(res, cst, "1.3.0")
	if len(rep.ByDate) != 1 || rep.ByDate[0].Issues != 1 {
		t.Fatalf("按日期合计没带上异常标的数：%+v", rep.ByDate)
	}
	if rep.Totals.BarsOff != 1 || rep.Totals.MismatchDays != 1 {
		t.Fatalf("两个跨标的的合计数没透传：%+v", rep.Totals)
	}
	if rep.ByDate[0].Majority != 276 || len(rep.ByDate[0].Counts) != 1 {
		t.Fatalf("多数派/根数分布没透传：%+v", rep.ByDate[0])
	}
}

func TestBuildEmptyResultMarshalsToEmptyArrays(t *testing.T) {
	// 前端直接 folders.length / notes.length / day.gaps.length 判断，
	// null 会让 Vue 模板里的 .length 报错。所以空的必须是 []，不是 null。
	rep := Build(&store.ScanResult{Root: "D:/data", Step: 300}, cst, "1.3.0")
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

	// 有日文件、但那天没有断档时，gaps / expect 也必须是空数组
	buf, err = json.Marshal(Build(sample(t), cst, "1.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), `"gaps":[`) || strings.Contains(string(buf), `"gaps":null`) {
		t.Fatalf("gaps 应当是数组：\n%s", buf)
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
