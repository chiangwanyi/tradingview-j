package store

// 增量扫描的测试。
//
// 网页版分析在抓取过程中要反复扫同一个目录（每两秒一次），所以 Scanner 会把
// 没变过的日文件「记」下来。这件事有两条必须钉死的性质：
//
//  1. **省活**：磁盘没动的文件不再解析。它省掉的正是全部收益，丢了这条就退化成
//     每次 1.2 秒的全量扫描，页面会开始卡、CPU 会一直转。
//  2. **不许省出偏差**：复用记忆得到的 ScanResult 必须和「完全重新扫一遍」逐字段
//     一模一样。这一类 bug 最阴 —— 页面上只是某一天的数字停住不动，看起来像没抓到，
//     实际是记忆把旧值当成了新值。

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// memoTree 造一个两标的、三天的数据目录。
func memoTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AAA", "2026-10-15.csv"), sampleDay)
	writeFile(t, filepath.Join(root, "AAA", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n2026-10-16 09:00,1,2,0.5,1.5,10\n")
	writeFile(t, filepath.Join(root, "BBB", "2026-10-16.csv"),
		"time,open,high,low,close,volume\n2026-10-16 09:00,1,2,0.5,1.5,10\n")
	return root
}

func TestScannerReusesUnchangedDays(t *testing.T) {
	root := memoTree(t)
	sc := NewScanner()

	first := sc.Scan(root, cst, 300)
	if got := sc.ParsedFiles(); got != 3 {
		t.Fatalf("第一次应当解析 3 份文件，实际 %d 份", got)
	}

	// 磁盘没动：一份都不该重解析，而结果必须和全量扫描逐字段相同
	again := sc.Scan(root, cst, 300)
	if got := sc.ParsedFiles(); got != 3 {
		t.Fatalf("磁盘没变却又解析了 %d 份（增量失效了）", got-3)
	}
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("复用记忆的结果与全量扫描不一致：\n首次 %+v\n再次 %+v", first, again)
	}

	// 抓回来一根：只有那一份文件该被重解析，根数要跟着变
	day := filepath.Join(root, "AAA", "2026-10-16.csv")
	writeFile(t, day, "time,open,high,low,close,volume\n"+
		"2026-10-16 09:00,1,2,0.5,1.5,10\n"+
		"2026-10-16 09:05,2,3,1.5,2.5,11\n")
	third := sc.Scan(root, cst, 300)
	if got := sc.ParsedFiles(); got != 4 {
		t.Fatalf("只改了一份文件，却多解析了 %d 份", got-3)
	}
	if third.Bars != first.Bars+1 {
		t.Fatalf("新加的一根没进统计：%d -> %d", first.Bars, third.Bars)
	}
	// 增量结果必须等于「从头扫一遍」
	if full := Scan(root, cst, 300); !reflect.DeepEqual(full, third) {
		t.Fatalf("增量结果与全量结果不一致：\n增量 %+v\n全量 %+v", third, full)
	}
}

func TestScannerForgetsDeletedFiles(t *testing.T) {
	root := memoTree(t)
	sc := NewScanner()
	first := sc.Scan(root, cst, 300)
	if first.Days != 3 {
		t.Fatalf("天数 = %d，期望 3", first.Days)
	}

	if err := os.Remove(filepath.Join(root, "AAA", "2026-10-15.csv")); err != nil {
		t.Fatal(err)
	}
	after := sc.Scan(root, cst, 300)
	if after.Days != 2 {
		t.Fatalf("删掉一天后天数 = %d，期望 2", after.Days)
	}
	// 记忆也要跟着缩：不然删了又建同名文件时，旧摘要会冒充新文件
	if _, ok := sc.memo[filepath.Join(root, "AAA", "2026-10-15.csv")]; ok {
		t.Fatal("已删除的文件还留在记忆里")
	}
}

// 周期（step）一改，「断档补几根」「Span 多少」全都不一样了，
// 上一步算出来的摘要一律作废 —— 否则页面上会出现「按 1 分钟算的断档」。
func TestScannerDropsMemoWhenStepChanges(t *testing.T) {
	root := memoTree(t)
	sc := NewScanner()
	sc.Scan(root, cst, 300)
	before := sc.ParsedFiles()

	sc.Scan(root, cst, 60)
	if got := sc.ParsedFiles() - before; got != 3 {
		t.Fatalf("周期变了却只重解析 %d 份，期望 3 份", got)
	}
	if !reflect.DeepEqual(sc.Scan(root, cst, 60), Scan(root, cst, 60)) {
		t.Fatal("换周期之后的增量结果与全量不一致")
	}
}

func TestScanWithoutMemoMatchesScanner(t *testing.T) {
	root := memoTree(t)
	if !reflect.DeepEqual(Scan(root, cst, 300), NewScanner().Scan(root, cst, 300)) {
		t.Fatal("包级 Scan 与 Scanner 的首次扫描结果不一致")
	}
}
