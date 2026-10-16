package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cst = time.FixedZone("UTC+8", 8*3600)

func newStore(t *testing.T, root string, header bool) *Store {
	t.Helper()
	s, err := Open(Options{Root: root, Header: header, Location: cst})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func readCSV(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	return string(b)
}

func TestSlugKeepsReadableParts(t *testing.T) {
	// 只替换文件系统不允许的字符，不做拼音/转写：冒号变下划线，感叹号保留。
	if got := slug("CME_MINI:MNQ1!"); got != "CME_MINI_MNQ1!" {
		t.Fatalf("slug = %q", got)
	}
	if got := slug("BINANCE:BTCUSDT"); got != "BINANCE_BTCUSDT" {
		t.Fatalf("slug = %q", got)
	}
	if got := slug("a/b\\c:d*e?f"); got != "a_b_c_d_e_f" {
		t.Fatalf("slug = %q", got)
	}
	if got := slug("  "); got != "unknown" {
		t.Fatalf("空名应回退为 unknown，实际 %q", got)
	}
}

func TestFolderCollisionGetsHashSuffix(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)

	a := s.Folder("CME_MINI:MNQ1!")
	if a != "CME_MINI_MNQ1!" {
		t.Fatalf("第一个标的的目录 = %q", a)
	}
	// Windows 文件名不区分大小写：这两个必须落到不同目录，绝不能互相覆盖
	b := s.Folder("cme_mini_mnq1!")
	if b == a || strings.EqualFold(b, a) {
		t.Fatalf("撞名没有处理: a=%q b=%q", a, b)
	}
	if !strings.HasPrefix(b, "cme_mini_mnq1!~") {
		t.Fatalf("撞名后应追加短哈希，实际 %q", b)
	}
	// 同一个标的第二次问，必须还是同一个目录（稳定）
	if again := s.Folder("CME_MINI:MNQ1!"); again != a {
		t.Fatalf("重复分配: %q != %q", again, a)
	}
	// 冒号和下划线 slug 化之后会撞车，也必须分开
	c := s.Folder("CME_MINI_MNQ1!")
	if strings.EqualFold(c, a) {
		t.Fatalf("不同标的落到同一目录: %q", c)
	}
}

func TestWritesDailyFileWithPlainTextTime(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)

	// 1792114800 = 2026-10-16 09:40 (+08:00)
	s.Add("CME_MINI:MNQ1!", Bar{T: 1792114800, O: 30067.25, H: 30067.25, L: 30056, C: 30061.5, V: 847, HasVolume: true})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(root, "CME_MINI_MNQ1!", "2026-10-16.csv")
	got := readCSV(t, path)
	want := "time,open,high,low,close,volume\n2026-10-16 09:40,30067.25,30067.25,30056,30061.5,847\n"
	if got != want {
		t.Fatalf("CSV 内容不符\n得到: %q\n期望: %q", got, want)
	}
	// 索引也要落盘，否则下次启动会重新猜目录名
	if _, err := os.Stat(filepath.Join(root, indexFile)); err != nil {
		t.Fatalf("没写 %s: %v", indexFile, err)
	}
}

func TestBarCrossingMidnightGoesToItsOwnDay(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)

	// +08:00 下的 2026-10-15 07:45 / 2026-10-16 09:40：两个不同的日文件
	s.Add("X", Bar{T: 1792021500, O: 1, H: 2, L: 0.5, C: 1.5})
	s.Add("X", Bar{T: 1792114800, O: 3, H: 4, L: 2.5, C: 3.5})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2026-10-15.csv", "2026-10-16.csv"} {
		if _, err := os.Stat(filepath.Join(root, "X", name)); err != nil {
			t.Fatalf("缺少 %s: %v", name, err)
		}
	}
	if got := readCSV(t, filepath.Join(root, "X", "2026-10-15.csv")); !strings.Contains(got, "2026-10-15 07:45,1,2,0.5,1.5") {
		t.Fatalf("15 日内容: %q", got)
	}
}

func TestSameTimestampIsOverwrittenNotAppended(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)

	// 正在走的那根 K 线会被 du 反复推送：先 30057.75，后 30061.5 —— 文件里只能有一行
	s.Add("X", Bar{T: 1792114800, O: 30067.25, H: 30067.25, L: 30056, C: 30057.75, V: 868, HasVolume: true})
	s.Add("X", Bar{T: 1792114800, O: 30067.25, H: 30067.25, L: 30056, C: 30061.5, V: 847, HasVolume: true})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, filepath.Join(root, "X", "2026-10-16.csv"))
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 { // 表头 + 一行
		t.Fatalf("行数 = %d, 期望 2：\n%s", len(lines), got)
	}
	if !strings.Contains(lines[1], "30061.5,847") {
		t.Fatalf("应当是后写覆盖前写，实际 %q", lines[1])
	}
}

func TestExistingFileIsLoadedBeforeRewrite(t *testing.T) {
	root := t.TempDir()

	// 第一次运行：写入一根
	s1 := newStore(t, root, true)
	s1.Add("X", Bar{T: 1792021500, O: 1, H: 2, L: 0.5, C: 1.5})
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// 第二次运行（模拟程序重启）：往同一天再写一根，之前那根必须还在
	s2 := newStore(t, root, true)
	s2.Add("X", Bar{T: 1792021800, O: 2, H: 3, L: 1.5, C: 2.5})
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	got := readCSV(t, filepath.Join(root, "X", "2026-10-15.csv"))
	if !strings.Contains(got, "2026-10-15 07:45,1,2,0.5,1.5") || !strings.Contains(got, "2026-10-15 07:50,2,3,1.5,2.5") {
		t.Fatalf("重启后旧数据被覆盖掉了:\n%s", got)
	}
}

func TestHeaderCanBeTurnedOff(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, false)
	s.Add("X", Bar{T: 1792114800, O: 1, H: 2, L: 0.5, C: 1.5})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, filepath.Join(root, "X", "2026-10-16.csv"))
	if strings.Contains(got, "time,open") {
		t.Fatalf("csvHeader=false 时不该写表头: %q", got)
	}
}

func TestMissingVolumeIsWrittenAsEmptyColumn(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)
	s.Add("X", Bar{T: 1792114800, O: 1, H: 2, L: 0.5, C: 1.5}) // 没有成交量
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, filepath.Join(root, "X", "2026-10-16.csv"))
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if lines[1] != "2026-10-16 09:40,1,2,0.5,1.5," {
		t.Fatalf("无成交量行 = %q", lines[1])
	}
}

func TestMalformedRowsAreSkippedNotFatal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "X")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "time,open,high,low,close,volume\n" +
		"2026-10-16 09:40,1,2,0.5,1.5,10\n" +
		"这不是时间,1,2,0.5,1.5,10\n" +
		"2026-10-16 09:45,abc,2,0.5,1.5,10\n" +
		"2026-10-16 09:50,2,3,1.5,2.5,11\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-10-16.csv"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newStore(t, root, true)
	s.Add("X", Bar{T: 1792115100, O: 3, H: 4, L: 2.5, C: 3.5}) // 触发载入 + 重写
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, filepath.Join(dir, "2026-10-16.csv"))
	if !strings.Contains(got, "09:40,1,2,0.5,1.5,10") || !strings.Contains(got, "09:50,2,3,1.5,2.5,11") {
		t.Fatalf("好行被坏行带崩了:\n%s", got)
	}
	if strings.Contains(got, "这不是时间") || strings.Contains(got, "abc") {
		t.Fatalf("坏行没有清掉:\n%s", got)
	}
}

func TestStatsReportsDistinctBars(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)
	// 同一根更新 5 次 + 另一根：Bars 应当是 2，Accepted 才是 6
	for i := 0; i < 5; i++ {
		s.Add("X", Bar{T: 1792114800, O: 1, H: 2, L: 0.5, C: 1.5 + float64(i)})
	}
	s.Add("X", Bar{T: 1792115100, O: 3, H: 4, L: 2.5, C: 3.5})

	st := s.Stats()
	if len(st) != 1 {
		t.Fatalf("统计条目数 = %d", len(st))
	}
	if st[0].Bars != 2 {
		t.Fatalf("Bars = %d, 期望 2（去重后的根数）", st[0].Bars)
	}
	if st[0].Accepted != 6 {
		t.Fatalf("Accepted = %d, 期望 6（含覆盖）", st[0].Accepted)
	}
	if st[0].FirstTime != "2026-10-16 09:40" || st[0].LastTime != "2026-10-16 09:45" {
		t.Fatalf("时间范围 %q ~ %q", st[0].FirstTime, st[0].LastTime)
	}
	if st[0].Symbol != "X" {
		t.Fatalf("Symbol = %q", st[0].Symbol)
	}
}

func TestLeftoverTempFileIsNotTreatedAsData(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root, true)
	s.Add("X", Bar{T: 1792114800, O: 1, H: 2, L: 0.5, C: 1.5})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "X", "2026-10-16.csv")); err != nil {
		t.Fatal(err)
	}
	// .tmp 是写盘中间态，正常情况下 rename 后就没了
	if _, err := os.Stat(filepath.Join(root, "X", "2026-10-16.csv.tmp")); err == nil {
		t.Fatal("临时文件没有 rename 掉")
	}
}
