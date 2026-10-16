package capture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tradingview-j/internal/config"
	"tradingview-j/internal/logx"
	"tradingview-j/internal/store"
	"tradingview-j/internal/wstest"
)

// 这个测试是一整套端到端验证，但**不启动任何浏览器**：
// 用一个假 CDP 服务端扮演 Chrome（应答 Target.getTargets / attachToTarget / Network.enable），
// 然后把真实抓包（testdata/real-capture.ndjson）当成 WebSocket 帧事件推下去，
// 最后断言 CSV 真的写出来了。
//
// 为什么要这么设计：需求明确禁止用无头浏览器做测试（慢、而且浏览器中断这类问题看不见）。
// 用假 CDP 服务端能得到同样的覆盖面 —— 除了「Chrome 的行为」以外，从分帧、
// 解码、周期判定、品种识别到落盘、留档，全部真跑了一遍。

const fixtureRel = "../../testdata/real-capture.ndjson"

// mixedFixtureRel：页面上同时有别的品种在走报价的真实抓包（这次「品种认错」的回归样本）
const mixedFixtureRel = "../../testdata/mixed-symbols.ndjson"

var testZone = time.FixedZone("UTC+8", 8*3600)

func loadFixtureFrames(t *testing.T) []string {
	t.Helper()
	return loadFixtureFramesFrom(t, fixtureRel)
}

func loadFixtureFramesFrom(t *testing.T, rel string) []string {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("打不开样本: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var rec struct {
			Kind string `json:"k"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Kind == "frame" && rec.Text != "" {
			out = append(out, rec.Text)
		}
	}
	if len(out) == 0 {
		t.Fatalf("样本 %s 里没有帧", rel)
	}
	return out
}

// fakeBrowser 是一个「会说话的」本地服务：既能应答 HTTP 的 /json/version，
// 也能升级成 WebSocket 跑 CDP 命令循环。
type fakeBrowser struct {
	port   int
	ln     net.Listener
	frames []string

	mu        sync.Mutex
	attached  bool
	networkOn bool
	pushes    int
}

func startFakeBrowser(t *testing.T, frames []string) *fakeBrowser {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	fb := &fakeBrowser{port: ln.Addr().(*net.TCPAddr).Port, ln: ln, frames: frames}
	go fb.serve()
	t.Cleanup(func() { ln.Close() })
	return fb
}

func (fb *fakeBrowser) serve() {
	for {
		c, err := fb.ln.Accept()
		if err != nil {
			return
		}
		go fb.handle(c)
	}
}

func (fb *fakeBrowser) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	tp := textproto.NewReader(br)
	reqLine, err := tp.ReadLine()
	if err != nil {
		return
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return
	}

	// 新版 Chrome 上 /json/version 是 404，但我们的发现逻辑会试它。
	// 这里让它成功，顺带把浏览器级 WS 路径给出去。
	if strings.Contains(reqLine, "/json/version") {
		body := fmt.Sprintf(`{"Browser":"FakeChrome/1.0","webSocketDebuggerUrl":"ws://127.0.0.1:%d/devtools/browser/fake"}`, fb.port)
		fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		return
	}

	key := hdr.Get("Sec-WebSocket-Key")
	if key == "" {
		return
	}
	h := sha1.New()
	io.WriteString(h, key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)

	for {
		op, payload, err := wstest.ReadFrame(br)
		if err != nil || op != wstest.OpText {
			return
		}
		var req struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return
		}
		var result any
		switch req.Method {
		case "Target.getTargets":
			result = map[string]any{"targetInfos": []map[string]any{
				{
					"targetId": "T1", "type": "page", "title": "MNQ1! 30,060.75",
					"url": "https://cn.tradingview.com/chart/FAKE/?symbol=CME_MINI%3AMNQ1%21",
				},
				{"targetId": "T2", "type": "page", "title": "别的不相干页面", "url": "https://example.com/"},
			}}
		case "Target.attachToTarget":
			fb.mu.Lock()
			fb.attached = true
			fb.mu.Unlock()
			result = map[string]any{"sessionId": "S1"}
		case "Network.enable":
			fb.mu.Lock()
			fb.networkOn = true
			fb.mu.Unlock()
			result = map[string]any{}
		default:
			result = map[string]any{}
		}
		resp, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
		if err := wstest.WriteText(c, string(resp)); err != nil {
			return
		}

		if req.Method == "Network.enable" {
			// 把真实抓包的帧一条条当成页面收到的 WebSocket 帧推下去
			for i, text := range fb.frames {
				event, _ := json.Marshal(map[string]any{
					"sessionId": "S1",
					"method":    "Network.webSocketFrameReceived",
					"params": map[string]any{
						"requestId": fmt.Sprintf("1000.%d", i),
						"timestamp": float64(1792115476 + i),
						"response": map[string]any{
							"opcode":      1,
							"mask":        false,
							"payloadData": text,
						},
					},
				})
				if err := wstest.WriteText(c, string(event)); err != nil {
					return
				}
				fb.mu.Lock()
				fb.pushes++
				fb.mu.Unlock()
			}
		}
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestEndToEndWithoutBrowser(t *testing.T) {
	frames := loadFixtureFrames(t)
	cfg, logs := runFakeSession(t, frames)

	// ---- 断言 ----

	symDir := filepath.Join(cfg.OutPath(), "CME_MINI_MNQ1!")
	entries, err := os.ReadDir(symDir)
	if err != nil {
		t.Fatalf("标的目录不存在: %v", err)
	}
	var dayFiles []string
	for _, e := range entries {
		dayFiles = append(dayFiles, e.Name())
	}
	if len(dayFiles) != 2 {
		t.Fatalf("日文件 = %v，期望 2 个（跨 10-15 / 10-16）", dayFiles)
	}

	if got := countRows(t, cfg.OutPath(), "CME_MINI_MNQ1!"); got != 300 {
		t.Fatalf("总行数 = %d，期望 300", got)
	}

	csvPath := filepath.Join(symDir, "2026-10-15.csv")
	content, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if lines[0] != "time,open,high,low,close,volume" {
		t.Fatalf("表头 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "2026-10-15 07:45,29932.75,29946.75,29885.75,29916,8858") {
		t.Fatalf("首行内容 = %q", lines[1])
	}
	for _, l := range lines {
		if strings.Contains(l, "1792021") || strings.Contains(l, "1792114") {
			t.Fatalf("时间列出现了 Unix 时间戳: %q", l)
		}
	}

	// 原始帧留档
	rawDir := filepath.Join(cfg.RawPath(), "CME_MINI_MNQ1!")
	raws, err := os.ReadDir(rawDir)
	if err != nil || len(raws) == 0 {
		t.Fatalf("原始帧留档缺失: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(rawDir, raws[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"~m~4~m~~h~1"`) {
		t.Fatalf("留档里没有心跳原始帧: %s", firstBytes(string(raw), 300))
	}
	// 第一条必须是 meta：留档文件要能自己说明来源
	if first, _, _ := strings.Cut(string(raw), "\n"); !strings.Contains(first, `"k":"meta"`) {
		t.Fatalf("留档第一条不是 meta: %q", first)
	} else if !strings.Contains(first, "CME_MINI") || !strings.Contains(first, "tradingview.com/chart/FAKE") {
		t.Fatalf("meta 里缺少来源信息: %s", first)
	}

	logText := logs.String()
	for _, want := range []string{"已接入图表页", "已由帧内 symbol_resolved 确认：CME_MINI:MNQ1!", "周期确认 5m"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("日志里缺少 %q\n---\n%s", want, logText)
		}
	}
	if strings.Contains(logText, "周期是 1m") || strings.Contains(logText, "跳过") {
		t.Fatalf("不该出现周期不符的告警:\n%s", logText)
	}
}

// TestMixedSymbolPageDoesNotMisFile 是这次「品种认错」的端到端回归：
// 样本页面上开着 CME_MINI:MNQ1! 的 5 分钟图，同时自选/侧边栏在给 9 个别的品种报价
// （其中 NASDAQ:NDX 被报价 5 次，正是把旧版本带跑偏的那条）。
//
// 断言：数据目录里**只有一个**标的文件夹，而且 300 行都在它下面。
// 旧版本会额外造出 data/NASDAQ_NDX/ —— 那个目录里装的其实也是 MNQ 的 K 线。
func TestMixedSymbolPageDoesNotMisFile(t *testing.T) {
	frames := loadFixtureFramesFrom(t, mixedFixtureRel)
	cfg, logs := runFakeSession(t, frames)

	entries, err := os.ReadDir(cfg.OutPath())
	if err != nil {
		t.Fatalf("数据目录不存在: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 || dirs[0] != "CME_MINI_MNQ1!" {
		t.Fatalf("标的目录 = %v，期望只有 CME_MINI_MNQ1!（旧版本会多出一个 NASDAQ_NDX）", dirs)
	}
	if got := countRows(t, cfg.OutPath(), "CME_MINI_MNQ1!"); got != 300 {
		t.Fatalf("总行数 = %d，期望 300", got)
	}
	// 最后一个时间戳那根（12:50）来自 NASDAQ:NDX 报价之后的 du —— 旧版本会写进错误目录
	content, err := os.ReadFile(filepath.Join(cfg.OutPath(), "CME_MINI_MNQ1!", "2026-10-16.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "2026-10-16 12:50,30189.75,30195.5,30184.5,30194,1656") {
		t.Fatalf("末根不在图表品种目录里:\n%s", firstBytes(string(content), 400))
	}

	logText := logs.String()
	if !strings.Contains(logText, "另有") || !strings.Contains(logText, "NASDAQ:NDX") {
		t.Fatalf("日志里应当说明页面上另有别的品种在走报价:\n%s", logText)
	}
	if strings.Contains(logText, "识别品种：NASDAQ:NDX") {
		t.Fatalf("把报价里的品种当成了图表品种:\n%s", logText)
	}
}

// runFakeSession 起假浏览器 + 常驻会话，把帧推完、等落盘稳定后收摊。
// 返回配置（用来找输出目录）与日志。
func runFakeSession(t *testing.T, frames []string) (*config.Config, *safeBuffer) {
	t.Helper()
	dir := t.TempDir()
	fb := startFakeBrowser(t, frames)

	cfg := config.Defaults()
	cfg.BaseDir = dir
	cfg.OutDir = "数据"
	cfg.RawDir = "留档"
	cfg.CDPPort = fb.port
	cfg.SaveRawFrames = true
	cfg.CSVHeader = true
	cfg.AttachPollSeconds = 0.05
	cfg.FlushSeconds = 0.05
	cfg.StatusSeconds = 0.2
	cfg.RetrySeconds = 0.05

	logs := &safeBuffer{}
	lg := logx.NewTo(logs)

	st, err := store.Open(store.Options{
		Root:     cfg.OutPath(),
		Header:   cfg.CSVHeader,
		Location: testZone,
		Log:      func(format string, args ...any) { lg.Info(format, args...) },
	})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(&cfg, st, lg).Run(ctx) }()

	waitFor(t, 20*time.Second, "帧推完", func() bool {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		return fb.pushes == len(frames)
	})
	// 帧推完不代表已经解码完：等落盘行数稳定下来再收摊
	waitStable(t, 10*time.Second, "CSV 行数", func() int { return countAllRows(t, cfg.OutPath()) })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 Run 没有返回（有协程卡住了）")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fb.mu.Lock()
	attached, networkOn, pushes := fb.attached, fb.networkOn, fb.pushes
	fb.mu.Unlock()
	if !attached || !networkOn || pushes != len(frames) {
		t.Fatalf("假浏览器状态：attached=%v networkOn=%v pushes=%d/%d", attached, networkOn, pushes, len(frames))
	}
	return &cfg, logs
}

func TestKeepsWaitingWhenNothingIsListening(t *testing.T) {
	dir := t.TempDir()
	// 挑一个几乎不可能被占用的端口，并且把 LOCALAPPDATA 指向空目录，
	// 这样「找不到 Chrome」这条路会被真实走到。
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "empty-localappdata"))

	cfg := config.Defaults()
	cfg.BaseDir = dir
	cfg.CDPPort = 59987
	cfg.RetrySeconds = 0.05
	cfg.StatusSeconds = 0.1

	logs := &safeBuffer{}
	lg := logx.NewTo(logs)
	st, err := store.Open(store.Options{Root: cfg.OutPath(), Header: true, Location: testZone})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := New(&cfg, st, lg).Run(ctx); err != nil {
		t.Fatalf("超时取消不该报错: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("等待期间耗时 %v，太久", elapsed)
	}

	text := logs.String()
	if !strings.Contains(text, "还没发现 Chrome 调试端点") {
		t.Fatalf("应当提示在等 Chrome:\n%s", text)
	}
	if n := strings.Count(text, "还没发现 Chrome 调试端点"); n > 2 {
		t.Fatalf("同一个诊断打印了 %d 次，刷屏了:\n%s", n, text)
	}
	// 等待期间不该产生任何数据文件
	if _, err := os.Stat(filepath.Join(cfg.OutPath(), "CME_MINI_MNQ1!")); err == nil {
		t.Fatal("没接入任何页面却创建了标的目录")
	}
}

func countRows(t *testing.T, root, folder string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, folder))
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".csv") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, folder, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" || strings.HasPrefix(line, "time,") {
				continue
			}
			total++
		}
	}
	return total
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("等 %s 超时（%s）", what, d)
}

// waitStable 等一个数字连续三次不变：帧都解码完、落盘也刷过一轮之后才收摊，
// 否则「等够了行数就断言」会在数据还没写完时误判。
func waitStable(t *testing.T, d time.Duration, what string, fn func() int) int {
	t.Helper()
	deadline := time.Now().Add(d)
	last, same := -1, 0
	for time.Now().Before(deadline) {
		cur := fn()
		if cur > 0 && cur == last {
			same++
			if same >= 3 {
				return cur
			}
		} else {
			same = 0
		}
		last = cur
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等 %s 稳定超时（最后 %d）", what, last)
	return last
}

// countAllRows 数整个数据根目录下的 K 线行数（跨所有标的文件夹），
// 用来观察「有没有数据被写进了别的标的目录」。
func countAllRows(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() {
			total += countRows(t, root, e.Name())
		}
	}
	return total
}

func firstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
