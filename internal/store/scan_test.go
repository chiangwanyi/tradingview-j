package store

// 只读扫描的测试。全部离线：不碰 Chrome，也不需要网络 ——
// 它读的就是落盘规则本身，所以直接造目录和 CSV 文件来验。

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 5 分钟级别的一小段：09:00 / 09:05 / 09:20 —— 中间缺两根。
const sampleDay = "time,open,high,low,close,volume\n" +
	"2026-10-16 09:00,1,2,0.5,1.5,10\n" +
	"2026-10-16 09:05,2,3,1.5,2.5,11\n" +
	"2026-10-16 09:20,3,4,2.5,3.5,12\n"

func TestScanCountsBarsAndGapsPerDay(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CME_MINI_MNQ1!", "2026-10-16.csv"), sampleDay)
	writeFile(t, filepath.Join(root, "CME_MINI_MNQ1!", "2026-10-15.csv"),
		"time,open,high,low,close,volume\n2026-10-15 09:00,1,2,0.5,1.5,\n")

	res := Scan(root, cst, 300)
	if len(res.Folders) != 1 {
		t.Fatalf("标的数 = %d", len(res.Folders))
	}
	f := res.Folders[0]
	if f.Folder != "CME_MINI_MNQ1!" || f.Symbol != "CME_MINI_MNQ1!" {
		t.Fatalf("文件夹/标的名 = %q / %q", f.Folder, f.Symbol)
	}
	if f.Known {
		t.Fatal("没有 _symbols.json 时不该认为索引里登记过")
	}
	if len(f.Days) != 2 {
		t.Fatalf("天数 = %d", len(f.Days))
	}
	// 文件名就是日期，必须按时间序排好
	if f.Days[0].Date != "2026-10-15" || f.Days[1].Date != "2026-10-16" {
		t.Fatalf("日期顺序不对: %q, %q", f.Days[0].Date, f.Days[1].Date)
	}

	d := f.Days[1]
	if d.Bars != 3 {
		t.Fatalf("根数 = %d, 期望 3", d.Bars)
	}
	// 09:00 -> 09:20 按 300 秒推算是 5 根，实有 3 根
	if d.Span != 5 || d.Missing != 2 {
		t.Fatalf("Span = %d, Missing = %d, 期望 5 / 2", d.Span, d.Missing)
	}
	if d.MaxGap != 900 {
		t.Fatalf("最大断档 = %d 秒, 期望 900（09:05 -> 09:20）", d.MaxGap)
	}
	// 断档要给出「缺的是哪一段」：09:05 与 09:20 之间少了 09:10、09:15 两根。
	// 边界取缺失那两根本身的时刻，而不是夹着它们的 09:05 / 09:20 —— 页面上印的就是这两个数。
	if len(d.Gaps) != 1 {
		t.Fatalf("断档段数 = %d, 期望 1", len(d.Gaps))
	}
	g := d.Gaps[0]
	if want := mustUnix(t, "2026-10-16 09:10", cst); g.StartT != want {
		t.Fatalf("缺失开始 = %s, 期望 09:10", Clock(g.StartT, cst))
	}
	if want := mustUnix(t, "2026-10-16 09:15", cst); g.EndT != want {
		t.Fatalf("缺失结束 = %s, 期望 09:15", Clock(g.EndT, cst))
	}
	if g.Bars != 2 {
		t.Fatalf("断档根数 = %d, 期望 2", g.Bars)
	}
	if want := mustUnix(t, "2026-10-16 09:00", cst); d.FirstT != want {
		t.Fatalf("首根时间戳 = %d, 期望 %d", d.FirstT, want)
	}
	if got := Clock(d.FirstT, cst); got != "09:00" {
		t.Fatalf("首根 = %q", got)
	}
	if got := Clock(d.LastT, cst); got != "09:20" {
		t.Fatalf("尾根 = %q", got)
	}
	if res.Bars != 4 || res.Days != 2 {
		t.Fatalf("合计 根数=%d 日文件=%d", res.Bars, res.Days)
	}
}

// mustUnix 用落盘时同一套规则算出明文时间对应的时间戳，
// 而不是把数字抄死 —— 抄死的话时区算错也发现不了。
func mustUnix(t *testing.T, stamp string, loc *time.Location) int64 {
	t.Helper()
	ts, err := time.ParseInLocation(timeLayout, stamp, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Unix()
}

func TestScanUsesSymbolIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, indexFile),
		`{"version":1,"symbols":{"CME_MINI:MNQ1!":{"folder":"CME_MINI_MNQ1!"}}}`)
	writeFile(t, filepath.Join(root, "CME_MINI_MNQ1!", "2026-10-16.csv"), sampleDay)
	// 索引里有、但目录里没数据（或反过来）都要能各自安好
	writeFile(t, filepath.Join(root, "BINANCE_BTCUSDT", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n2026-10-16 09:00,1,2,0.5,1.5,10\n")

	res := Scan(root, cst, 300)
	if len(res.Folders) != 2 {
		t.Fatalf("标的数 = %d", len(res.Folders))
	}
	// 排序后是 BINANCE_BTCUSDT、CME_MINI_MNQ1!
	if res.Folders[0].Symbol != "BINANCE_BTCUSDT" || res.Folders[0].Known {
		t.Fatalf("没登记的目录应当用文件夹名当标的：%q known=%v", res.Folders[0].Symbol, res.Folders[0].Known)
	}
	// 索引只认「文件夹 -> 标的」，别指望从目录名反推出冒号
	if res.Folders[1].Symbol != "CME_MINI:MNQ1!" || !res.Folders[1].Known {
		t.Fatalf("标的 = %q known=%v", res.Folders[1].Symbol, res.Folders[1].Known)
	}
}

func TestScanFlagsDuplicateRows(t *testing.T) {
	root := t.TempDir()
	// 正常写入是「整体重写 + 原子改名」，同一时间戳不可能出现两次；
	// 真出现就说明文件被手工动过，必须报出来，不能悄悄去重了事。
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n"+
			"2026-10-16 09:00,1,2,0.5,1.5,10\n"+
			"2026-10-16 09:00,9,9,9,9,99\n")

	d := Scan(root, cst, 300).Folders[0].Days[0]
	if d.Rows != 2 || d.Bars != 1 || d.Dupes != 1 {
		t.Fatalf("行数/根数/重复 = %d / %d / %d, 期望 2 / 1 / 1", d.Rows, d.Bars, d.Dupes)
	}
}

func TestScanIgnoresNonCSVAndSumsAcrossSymbols(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"), sampleDay)
	writeFile(t, filepath.Join(root, "B", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n2026-10-16 09:00,1,2,0.5,1.5,10\n")
	// 写盘中间态、留档目录、索引文件都不该被算成数据
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv.tmp"), sampleDay)
	writeFile(t, filepath.Join(root, "_symbols.json"), `{"version":1,"symbols":{}}`)
	writeFile(t, filepath.Join(root, "raw", "A", "x.ndjson"), "{}\n")

	res := Scan(root, cst, 300)
	if len(res.Folders) != 2 {
		t.Fatalf("标的数 = %d, 期望 2（A、B）", len(res.Folders))
	}
	if res.Bars != 4 || res.Days != 2 {
		t.Fatalf("合计 根数=%d 日文件=%d, 期望 4 / 2", res.Bars, res.Days)
	}

	rows := res.ByDate()
	if len(rows) != 1 || rows[0].Date != "2026-10-16" || rows[0].Symbols != 2 || rows[0].Bars != 4 {
		t.Fatalf("按日期合计 = %+v", rows)
	}
	// 这一天两个标的的根数不一样（3 / 1），而且都不等于周五应有的 276 根 ——
	// 新增的两条判据都会亮，顺带钉住「跨标的比较只比当天有数据的标的」。
	if !rows[0].Mismatch || rows[0].Issues != 2 {
		t.Fatalf("按日期合计 = %+v，期望两个标的都不符预期、且互相不一致", rows[0])
	}
}

func TestByDateCountsSymbolsWithIssues(t *testing.T) {
	root := t.TempDir()
	// A 干净，B 有坏行 —— 这一天应当记「1 个标的带异常」，而不是把坏行数加进去。
	// 两份都用「根数正好对得上」的满一天，免得根数判定一起亮，把被测的那件事盖住。
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"), fullDayCSV("2026-10-16"))
	writeFile(t, filepath.Join(root, "B", "2026-10-16.csv"),
		fullDayCSV("2026-10-16")+"这不是时间,1,2,0.5,1.5,10\n")

	rows := Scan(root, cst, 300).ByDate()
	if len(rows) != 1 || rows[0].Issues != 1 {
		t.Fatalf("按日期合计的异常标的数 = %+v, 期望 1", rows)
	}
	// 两份文件根数一样，就不该判成「不一致」
	if rows[0].Mismatch {
		t.Fatalf("根数相同不该算不一致：%+v", rows[0])
	}
}

// fullDayCSV 造一份「根数正好等于工作日应有根数」的日文件：00:00~23:55 按 5 分钟铺满，
// 跳过 05:00~05:55（CME 每日结算休市 1 小时 = 12 根），一共 288-12 = 276 根。
// 有些用例要验的**不是**根数判定，这时得让数据本身干净，否则满屏都是新加的异常标记。
func fullDayCSV(date string) string {
	var b strings.Builder
	b.WriteString("time,open,high,low,close,volume\n")
	for i := 0; i < 288; i++ {
		if i >= 60 && i < 72 {
			continue
		}
		m := i * 5
		fmt.Fprintf(&b, "%s %02d:%02d,1,2,0.5,1.5,10\n", date, m/60, m%60)
	}
	return b.String()
}

// 「这一天应当有多少根」只由星期几决定，与文件里读到几根无关。
// 这份表是本机 CME 作息的实测结论，写错了会让日历天天误报，所以单独钉住。
func TestExpectedBarsByWeekday(t *testing.T) {
	cases := []struct {
		date string
		want []int
	}{
		{"2026-10-19", []int{216, 204}}, // 周一：夏令时 18 小时 / 冬令时 17 小时
		{"2026-10-20", []int{276}},      // 周二
		{"2026-10-21", []int{276}},      // 周三
		{"2026-10-22", []int{276}},      // 周四
		{"2026-10-23", []int{276}},      // 周五
		{"2026-10-24", []int{72, 60}},   // 周六：冬令时 6 小时 / 夏令时 5 小时
		{"2026-10-25", []int{0}},        // 周日：休市
		{"不是日期", nil},                 // 认不出来就不判，不误报
	}
	for _, c := range cases {
		got := ExpectedBars(c.date, cst)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("ExpectedBars(%s) = %v, 期望 %v", c.date, got, c.want)
		}
	}
	// 判定表本身也要能被页面原样印出来（HTML 里不再抄一份数字）
	rule := ExpectedRule()
	if len(rule) != 7 || rule[0].Weekday != "周一" {
		t.Fatalf("判定表 = %+v", rule)
	}
}

// 根数不符该星期应有根数 = 异常；这是这一版新增的主判据。
func TestScanFlagsBarsOff(t *testing.T) {
	root := t.TempDir()
	// 2026-10-16 是周五，应当 276 根；这里只有 3 根
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"), sampleDay)
	d := Scan(root, cst, 300).Folders[0].Days[0]
	if !d.BarsOff {
		t.Fatalf("周五 3 根应当判成根数不符：%+v", d)
	}
	if !d.Anomaly() {
		t.Fatal("根数不符要算异常（日历上标红的就是它）")
	}
	if len(d.Expect) != 1 || d.Expect[0] != 276 {
		t.Fatalf("应有根数没算出来：%v", d.Expect)
	}
	if d.Mismatch {
		t.Fatal("只有一个标的时不该判成「与其他标的不一致」")
	}
}

// 跨标的的根数比较：同一批数据里各标的的作息并不完全一致（休市日黄金比股指早收），
// 这是值得单独标出来的一条信号。
func TestScanFlagsCrossSymbolMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"), sampleDay) // 3 根
	writeFile(t, filepath.Join(root, "B", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n2026-10-16 09:00,1,2,0.5,1.5,10\n") // 1 根
	// B 没有 10-15 这一天的文件：不能当成它抓了 0 根，否则「缺抓」会被误报成不一致
	writeFile(t, filepath.Join(root, "A", "2026-10-15.csv"),
		"time,open,high,low,close,volume\n2026-10-15 09:00,1,2,0.5,1.5,10\n")

	res := Scan(root, cst, 300)
	if res.MismatchDays != 1 {
		t.Fatalf("不一致的天数 = %d, 期望 1（只有 10-16）", res.MismatchDays)
	}
	if res.BarsOff != 3 {
		t.Fatalf("根数不符的日文件数 = %d, 期望 3（两个标的各一天，都不等于该星期应有根数）", res.BarsOff)
	}
	for _, f := range res.Folders {
		for _, d := range f.Days {
			want := d.Date == "2026-10-16"
			if d.Mismatch != want {
				t.Fatalf("%s/%s 的 Mismatch = %v, 期望 %v", f.Folder, d.Date, d.Mismatch, want)
			}
		}
	}

	rows := res.ByDate()
	if len(rows) != 2 {
		t.Fatalf("按日期合计 = %+v", rows)
	}
	// 10-15 只有 A 有数据：一个标的谈不上「不一致」
	if rows[0].Date != "2026-10-15" || rows[0].Mismatch || rows[0].Majority != 1 {
		t.Fatalf("10-15 = %+v", rows[0])
	}
	// 10-16 两个标的、两个不同的根数：多数派并列时取根数大的那个
	if !rows[1].Mismatch || rows[1].Majority != 3 {
		t.Fatalf("10-16 = %+v", rows[1])
	}
	if len(rows[1].Counts) != 2 || rows[1].Counts[0].Bars != 3 || rows[1].Counts[0].Symbols != 1 {
		t.Fatalf("根数分布 = %+v", rows[1].Counts)
	}
}

func TestScanIsReadOnlyAndTolerant(t *testing.T) {
	root := t.TempDir()
	// 坏行只跳掉自己，不拖垮整份文件
	writeFile(t, filepath.Join(root, "A", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n"+
			"2026-10-16 09:00,1,2,0.5,1.5,10\n"+
			"这不是时间,1,2,0.5,1.5,10\n"+
			"2026-10-16 09:05,abc,2,0.5,1.5,10\n")
	before := snapshot(t, root)

	res := Scan(root, cst, 300)
	if res.Skipped != 2 {
		t.Fatalf("坏行数 = %d, 期望 2", res.Skipped)
	}
	if res.Folders[0].Days[0].Bars != 1 {
		t.Fatalf("好行没统计到：%d", res.Folders[0].Days[0].Bars)
	}
	if got := snapshot(t, root); got != before {
		t.Fatalf("扫描动了数据目录：\n前 %s\n后 %s", before, got)
	}

	// 数据目录不存在：不是错误，只给一条说明
	missing := Scan(filepath.Join(root, "不存在"), cst, 300)
	if missing.Exists || len(missing.Notes) == 0 || len(missing.Folders) != 0 {
		t.Fatalf("目录不存在时应当只报说明：%+v", missing)
	}
}

// snapshot 列出目录里所有文件（含大小），用来证明扫描是只读的。
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var out string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out += rel + ":" + info.Mode().String() + "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
