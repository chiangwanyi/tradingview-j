package webreport

// HTTP 层：接口能取到、取到的数字和 store.Scan 一致、取完之后磁盘**没被动过**。

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const testIndex = `{
  "version": 1,
  "symbols": {
    "CME_MINI:MES1!": { "folder": "CME_MINI_MES1!" }
  }
}`

// 3 根：00:00 / 00:05 / 00:15 —— 中间少了 00:10 一根（断档 1 处），
// 而 2026-10-16 是周五、应当 276 根，所以这一天同时是「根数不符」的异常日。
const testCSV = "time,open,high,low,close,volume\n" +
	"2026-10-16 00:00,1,2,0.5,1.5,10\n" +
	"2026-10-16 00:05,1.5,2,1,1.8,11\n" +
	"2026-10-16 00:15,1.8,3,1.7,2.5,12\n"

// fakeData 造一个最小但真实的数据目录：一个标的文件夹、一份日文件、一份索引。
func fakeData(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "CME_MINI_MES1!")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-16.csv"), []byte(testCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	// 顺手放一个不是数据的文件：扫描只认 .csv，它不该影响任何数字
	if err := os.WriteFile(filepath.Join(dir, "2026-10-16.csv.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "_symbols.json"), []byte(testIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// snapshot 记下目录里每个文件的相对路径 + 大小。
// 扫描前后比对它，钉住「网页版只读」这条性质 —— 页面刷新几次不该改任何文件。
func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if fi.IsDir() {
			out = append(out, "d "+rel)
			return nil
		}
		out = append(out, "f "+rel+" "+strconv.FormatInt(fi.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestAnalysisEndpointReportsSameNumbersAsScan(t *testing.T) {
	root := fakeData(t)
	before := snapshot(t, root)

	srv := httptest.NewServer(New(Options{Root: root, Step: 300, Location: cst, Version: "test"}))
	defer srv.Close()

	code, body := get(t, srv.URL+"/api/analysis")
	if code != 200 {
		t.Fatalf("状态码 = %d：%s", code, body)
	}
	var rep Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("返回的不是 JSON：%v\n%s", err, body)
	}

	if rep.Meta.Root != root {
		t.Fatalf("root = %q，期望 %q", rep.Meta.Root, root)
	}
	if !rep.Meta.Exists {
		t.Fatal("数据目录存在，Exists 却是 false")
	}
	if rep.Meta.GeneratedAt <= 0 {
		t.Fatal("没带生成时刻，页面上的「快照时间」就没了")
	}
	if rep.Totals.Bars != 3 || rep.Totals.Days != 1 || rep.Totals.Symbols != 1 {
		t.Fatalf("合计不对：%+v", rep.Totals)
	}
	if rep.Totals.Skipped != 0 || rep.Totals.Dupes != 0 {
		t.Fatalf("干净数据不该有坏行/重复行：%+v", rep.Totals)
	}

	if len(rep.Folders) != 1 {
		t.Fatalf("标的数 = %d", len(rep.Folders))
	}
	fo := rep.Folders[0]
	if fo.Symbol != "CME_MINI:MES1!" || !fo.Known {
		t.Fatalf("索引没被用上：%+v", fo)
	}
	if len(fo.Files) != 1 {
		t.Fatalf("日文件数 = %d（.tmp 不该被算进来）", len(fo.Files))
	}
	d := fo.Files[0]
	if d.Bars != 3 || d.Rows != 3 {
		t.Fatalf("日文件数字不对：%+v", d)
	}
	// 00:05 -> 00:15 之间少了 00:10 一根，断档边界取的就是那根缺失 K 线自己的时刻
	if len(d.Gaps) != 1 || d.Gaps[0].Bars != 1 {
		t.Fatalf("断档不对：%+v", d.Gaps)
	}
	if !d.BarsOff || !d.Anomaly {
		t.Fatalf("3 根不等于周五应有的 276 根，应当判成异常：%+v", d)
	}

	// 「缺口 / 最大断档」这两个概念已经整个从报告里删掉了 —— 字段不该再出现在响应里，
	// 否则前端可能又照着老字段渲染一遍，而那两个数字恰恰是这一版要去掉的东西。
	for _, gone := range []string{`"missing"`, `"maxGap"`} {
		if strings.Contains(body, gone) {
			t.Fatalf("响应里还有 %s 字段：\n%s", gone, body)
		}
	}
	if !strings.Contains(body, `"expectRule"`) || !strings.Contains(body, `"gaps"`) {
		t.Fatalf("新字段没进响应：\n%s", body)
	}

	if len(rep.ByDate) != 1 || rep.ByDate[0].Bars != 3 || rep.ByDate[0].Symbols != 1 {
		t.Fatalf("按日期合计不对：%+v", rep.ByDate)
	}

	// 只读：走完一次完整的分析，目录里不该多出、少掉、或者变大变小任何东西
	if got := snapshot(t, root); !equalStrings(before, got) {
		t.Fatalf("分析动了数据目录：\n前：%v\n后：%v", before, got)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	root := fakeData(t)
	srv := httptest.NewServer(New(Options{Root: root, Step: 300, Location: cst, Version: "test"}))
	defer srv.Close()

	// 首页
	code, body := get(t, srv.URL+"/")
	if code != 200 || !strings.Contains(body, `id="app"`) {
		t.Fatalf("首页不对（%d）：%s", code, body)
	}
	if !strings.Contains(body, "/static/app.js") || !strings.Contains(body, "/static/vendor/vue.global.prod.js") {
		t.Fatal("首页没引用前端脚本")
	}

	// 自己写的三个文件
	for _, c := range []struct{ path, want string }{
		{"/static/app.js", "createApp"},
		{"/static/style.css", "--bg"},
		{"/static/vendor/README.md", "unpkg"},
	} {
		code, body := get(t, srv.URL+c.path)
		if code != 200 {
			t.Fatalf("%s 状态码 = %d", c.path, code)
		}
		if !strings.Contains(body, c.want) {
			t.Fatalf("%s 的内容里没有 %q（内嵌资源是不是没更新？）", c.path, c.want)
		}
	}

	// 两个第三方库：只断言「取到了、而且不是空壳」。
	// 具体大小会随版本变，钉死数字只会让升级时白改一次测试。
	for _, p := range []string{"/static/vendor/vue.global.prod.js", "/static/vendor/tailwind.js"} {
		code, body := get(t, srv.URL+p)
		if code != 200 || len(body) < 50*1024 {
			t.Fatalf("%s 取不到或不完整（%d，%d 字节）", p, code, len(body))
		}
	}

	// 不认识的路径要 404，而不是把首页塞回去（否则前端路由会看到一堆 HTML 当 JSON 解）
	if code, _ := get(t, srv.URL+"/不存在的路径"); code != 404 {
		t.Fatalf("未知路径状态码 = %d，期望 404", code)
	}
}

func TestStartReportsPortFallback(t *testing.T) {
	// 先占住一个端口，再让 Start 用同一个端口 —— 应当换端口并如实报告，
	// 而不是静默换掉（用户是拿这个端口号去浏览器里敲地址的）。
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	srv, err := Start(Options{Root: fakeData(t), Step: 300, Location: cst, Version: "test"}, port)
	if err != nil {
		t.Fatalf("端口被占用时不该直接失败：%v", err)
	}
	defer srv.Close()

	if !srv.Moved {
		t.Fatal("换了端口却没说")
	}
	if srv.Port == port {
		t.Fatalf("还是用了被占用的端口 %d", port)
	}
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Fatalf("只该监听回环地址，实际是 %q", srv.URL)
	}
	code, body := get(t, srv.URL+"api/analysis")
	if code != 200 || !strings.Contains(body, `"totals"`) {
		t.Fatalf("换端口后的服务不通（%d）：%s", code, body)
	}
}

func TestStartWithEphemeralPort(t *testing.T) {
	srv, err := Start(Options{Root: fakeData(t), Step: 300, Location: cst, Version: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.Moved || srv.Port <= 0 {
		t.Fatalf("port=0 应当直接要一个空闲端口：%+v", srv)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
