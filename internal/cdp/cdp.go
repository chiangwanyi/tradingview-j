// Package cdp 负责接入本机正在运行的 Chrome，并把 CDP 的多路会话封装成同步调用。
//
// 两个来自实测的硬约束（都在 README 里记着）：
//
//  1. **Chrome >= 136 起，给默认用户数据目录加 --remote-debugging-port 会被静默忽略**，
//     所以不能靠「重启一个带调试端口的 Chrome」，只能从
//     <User Data>/DevToolsActivePort 里读端口和浏览器级 WS 路径。
//
//  2. **新版 Chrome 只开放浏览器级 WebSocket**：HTTP 的 /json/version 与 /json/list 一律 404。
//     所以页面发现走 Target.getTargets，而不是 HTTP 列表接口。
//     /json/version 仍然会试一次 —— 成功就拿版本号，失败属于预期，不是错误。
//
// 一条 WebSocket 上复用多个会话（flatten 模式）：每次 attachToTarget 拿一个 sessionId，
// 之后所有命令和事件都带这个 sessionId 区分。这样同时挂多个图表页只占一条连接。
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tradingview-j/internal/wsclient"
)

const defaultTimeout = 20 * time.Second

// 常见浏览器的用户数据目录（相对 %LOCALAPPDATA%）
var browserProfiles = []struct {
	ID   string
	Rel  string
	Name string
}{
	{"chrome", "Google/Chrome/User Data", "Chrome"},
	{"chrome-beta", "Google/Chrome Beta/User Data", "Chrome Beta"},
	{"chrome-canary", "Google/Chrome SxS/User Data", "Chrome Canary"},
	{"chromium", "Chromium/User Data", "Chromium"},
	{"edge", "Microsoft/Edge/User Data", "Edge"},
}

var defaultPorts = []int{9222, 9229, 9333}

type PortFile struct {
	Browser string
	Path    string
	Port    int
	WSPath  string
	Err     string
}

type Endpoint struct {
	Port          int
	BrowserWSURL  string
	Browser       string
	BrowserName   string
	Source        string // 端口是从哪来的：配置 / DevToolsActivePort / 端口探测
	Version       string // 形如 "Chrome/155.0.0.0"，拿不到就是空
	HTTPDiscovery bool   // /json/version 是否可用（新版 Chrome 恒 false，属预期）
}

func (e *Endpoint) String() string {
	s := fmt.Sprintf("port %d · 来源 %s", e.Port, e.Source)
	if e.Version != "" {
		s += " · " + e.Version
	}
	if !e.HTTPDiscovery {
		s += " · HTTP 发现接口不可用（新版 Chrome 预期行为）"
	}
	return s
}

// NotFoundError 带上「试过哪些端口、找到了哪些 DevToolsActivePort 文件」，
// 让上层能打印一份可操作的诊断，而不是一句「没找到」。
type NotFoundError struct {
	Candidates []string
	PortFiles  []PortFile
}

func (e *NotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("没有发现可用的 Chrome 调试端点")
	for _, c := range e.Candidates {
		b.WriteString("\n    · ")
		b.WriteString(c)
	}
	if len(e.PortFiles) == 0 {
		b.WriteString("\n    没找到任何 DevToolsActivePort 文件")
	} else {
		for _, pf := range e.PortFiles {
			if pf.Err != "" {
				b.WriteString(fmt.Sprintf("\n    %s 读取失败：%s", pf.Path, pf.Err))
				continue
			}
			b.WriteString(fmt.Sprintf("\n    找到 %s（端口 %d）", pf.Path, pf.Port))
		}
	}
	return b.String()
}

type DiscoverOptions struct {
	Port int // 来自配置；>0 时优先只用它
	Log  func(format string, args ...any)
}

// Discover 找到浏览器级调试端点。失败返回 *NotFoundError（里面带诊断信息）。
func Discover(opts DiscoverOptions) (*Endpoint, error) {
	var tried []string
	portFiles := readPortFiles()

	type cand struct {
		port    int
		wsPath  string
		source  string
		browser string
		name    string
	}
	var cands []cand
	if opts.Port > 0 {
		cands = append(cands, cand{port: opts.Port, source: "配置 cdpPort"})
	}
	for _, pf := range portFiles {
		if pf.Port == 0 || pf.Err != "" {
			continue
		}
		cands = append(cands, cand{port: pf.Port, wsPath: pf.WSPath, source: "DevToolsActivePort", browser: pf.Browser, name: browserName(pf.Browser)})
	}
	for _, p := range defaultPorts {
		dup := false
		for _, c := range cands {
			if c.port == p {
				dup = true
				break
			}
		}
		if !dup {
			cands = append(cands, cand{port: p, source: "端口探测"})
		}
	}

	for _, c := range cands {
		if !tcpProbe(c.port) {
			tried = append(tried, fmt.Sprintf("端口 %d（%s）：无监听", c.port, c.source))
			continue
		}
		version, vInfo := jsonGet(fmt.Sprintf("http://127.0.0.1:%d/json/version", c.port))
		wsPath := c.wsPath
		if wsPath == "" && vInfo != "" {
			if u := parseWSPath(vInfo); u != "" {
				wsPath = u
			}
		}
		if wsPath == "" {
			tried = append(tried, fmt.Sprintf("端口 %d（%s）：在监听，但拿不到浏览器级 WS 路径", c.port, c.source))
			continue
		}
		return &Endpoint{
			Port:          c.port,
			BrowserWSURL:  fmt.Sprintf("ws://127.0.0.1:%d%s", c.port, wsPath),
			Browser:       c.browser,
			BrowserName:   c.name,
			Source:        c.source,
			Version:       version,
			HTTPDiscovery: version != "",
		}, nil
	}

	return nil, &NotFoundError{Candidates: tried, PortFiles: portFiles}
}

func browserName(id string) string {
	for _, p := range browserProfiles {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

// readPortFiles 读 <User Data>/DevToolsActivePort：第一行端口，第二行浏览器级 WS 路径。
func readPortFiles() []PortFile {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	var out []PortFile
	for _, p := range browserProfiles {
		path := filepath.Join(base, filepath.FromSlash(p.Rel), "DevToolsActivePort")
		pf := PortFile{Browser: p.ID, Path: path}
		raw, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				pf.Err = err.Error()
				out = append(out, pf)
			}
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) == 0 {
			continue
		}
		var port int
		fmt.Sscanf(strings.TrimSpace(lines[0]), "%d", &port)
		if port <= 0 || port > 65535 {
			pf.Err = "端口字段不合法"
			out = append(out, pf)
			continue
		}
		pf.Port = port
		if len(lines) > 1 {
			pf.WSPath = strings.TrimSpace(lines[1])
		}
		out = append(out, pf)
	}
	return out
}

func tcpProbe(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// jsonGet 返回 (Browser 版本串, webSocketDebuggerUrl)。
// 新版 Chrome 上这两者都是空的 —— 属预期，调用方不该当错误处理。
func jsonGet(url string) (string, string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var v struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", ""
	}
	return v.Browser, v.WebSocketDebuggerURL
}

func parseWSPath(raw string) string {
	idx := strings.Index(raw, "/devtools/")
	if idx < 0 {
		return ""
	}
	path := raw[idx:]
	path = strings.TrimRight(path, "/")
	return path
}

// ---------------- 传输层 ----------------

type eventHandler func(sessionID, method string, params json.RawMessage)

type pendingCall struct {
	method string
	ch     chan callResult
}

type callResult struct {
	result json.RawMessage
	err    error
}

type Transport struct {
	conn *wsclient.Conn

	mu      sync.Mutex
	seq     int64
	pending map[int64]pendingCall
	handler eventHandler

	done     chan struct{}
	closeErr error
}

func DialTransport(wsURL string, opts wsclient.Options) (*Transport, error) {
	conn, err := wsclient.Dial(wsURL, opts)
	if err != nil {
		return nil, err
	}
	t := &Transport{
		conn:    conn,
		pending: map[int64]pendingCall{},
		done:    make(chan struct{}),
	}
	go t.readLoop()
	return t, nil
}

func (t *Transport) Done() <-chan struct{} { return t.done }

func (t *Transport) Err() error {
	select {
	case <-t.done:
		return t.closeErr
	default:
		return nil
	}
}

// OnEvent 注册事件处理器。
//
// 注意：处理器在**读协程**里同步执行，所以它绝不能回头调用 Call —— 那会自己等自己。
// 需要发命令（attach / Network.enable）的逻辑放在独立的监督协程里。
func (t *Transport) OnEvent(fn eventHandler) { t.handler = fn }

func (t *Transport) readLoop() {
	var err error
	for {
		op, data, rerr := t.conn.ReadMessage()
		if rerr != nil {
			err = rerr
			break
		}
		if op != wsclient.OpText {
			continue
		}
		if err := t.dispatch(data); err != nil {
			// 单条消息解析失败不该干掉整条连接：CDP 上偶尔会有超长/异常的负载
			continue
		}
	}
	t.mu.Lock()
	if t.closeErr == nil {
		t.closeErr = err
	}
	for id, p := range t.pending {
		select {
		case p.ch <- callResult{err: fmt.Errorf("%s: 连接已关闭（%v）", p.method, err)}:
		default:
		}
		delete(t.pending, id)
	}
	t.mu.Unlock()
	close(t.done)
}

type wireMessage struct {
	ID        *int64          `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (t *Transport) dispatch(data []byte) error {
	var msg wireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	if msg.ID != nil {
		t.mu.Lock()
		p, ok := t.pending[*msg.ID]
		delete(t.pending, *msg.ID)
		t.mu.Unlock()
		if !ok {
			return nil
		}
		if msg.Error != nil {
			p.ch <- callResult{err: fmt.Errorf("%s 失败: %s", p.method, msg.Error.Message)}
		} else {
			p.ch <- callResult{result: msg.Result}
		}
		return nil
	}
	if msg.Method != "" && t.handler != nil {
		t.handler(msg.SessionID, msg.Method, msg.Params)
	}
	return nil
}

// Call 发一条命令并等结果。sessionID 为空表示发给浏览器级会话。
func (t *Transport) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = buf
	}

	t.mu.Lock()
	select {
	case <-t.done:
		err := t.closeErr
		t.mu.Unlock()
		return nil, fmt.Errorf("%s: 连接已关闭（%v）", method, err)
	default:
	}
	t.seq++
	id := t.seq
	ch := make(chan callResult, 1)
	t.pending[id] = pendingCall{method: method, ch: ch}
	t.mu.Unlock()

	frame := map[string]any{"id": id, "method": method}
	if sessionID != "" {
		frame["sessionId"] = sessionID
	}
	if raw != nil {
		frame["params"] = raw
	}
	buf, err := json.Marshal(frame)
	if err != nil {
		t.forget(id)
		return nil, err
	}
	if err := t.conn.WriteText(buf); err != nil {
		t.forget(id)
		return nil, fmt.Errorf("发送 %s 失败: %w", method, err)
	}

	timer := time.NewTimer(defaultTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.result, r.err
	case <-timer.C:
		t.forget(id)
		return nil, fmt.Errorf("%s 超时（%s）", method, defaultTimeout)
	case <-ctx.Done():
		t.forget(id)
		return nil, ctx.Err()
	}
}

func (t *Transport) forget(id int64) {
	t.mu.Lock()
	delete(t.pending, id)
	t.mu.Unlock()
}

func (t *Transport) Close() { t.conn.Close() }

// ---------------- 目标页 ----------------

type Target struct {
	ID       string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Attached bool   `json:"attached"`
}

func (t *Transport) GetTargets(ctx context.Context) ([]Target, error) {
	raw, err := t.Call(ctx, "", "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		TargetInfos []Target `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.TargetInfos, nil
}

func (t *Transport) Attach(ctx context.Context, targetID string) (string, error) {
	raw, err := t.Call(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return "", err
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if res.SessionID == "" {
		return "", errors.New("attachToTarget 没返回 sessionId")
	}
	return res.SessionID, nil
}

func (t *Transport) Detach(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	_, _ = t.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": sessionID})
}

// EnableNetwork 打开网络事件。缓冲给大是因为图表页一帧能到几百 KB，
// 而且历史数据是整段整段回来的。
func (t *Transport) EnableNetwork(ctx context.Context, sessionID string, bufferMB int) error {
	if bufferMB <= 0 {
		bufferMB = 200
	}
	_, err := t.Call(ctx, sessionID, "Network.enable", map[string]any{
		"maxTotalBufferSize":    bufferMB * 1024 * 1024,
		"maxResourceBufferSize": 20 * 1024 * 1024,
		"maxPostDataSize":       1024 * 1024,
	})
	return err
}

// ---------------- 页面挑选 ----------------

func IsPage(t Target) bool { return t.Type == "page" && t.ID != "" }

// MatchTarget 按 URL / 标题子串挑页面，分高者胜。
// 与旧版 Node 实现保持同样的加权规则（命中越靠前的关键词权重越高，
// 并且 tradingview.com/chart 额外加分），这样「同时开了好几个页面」时选中的还是同一个。
func MatchTarget(targets []Target, needles []string) *Target {
	var best *Target
	bestScore := 0
	for i := range targets {
		t := targets[i]
		if !IsPage(t) {
			continue
		}
		hay := t.URL + " " + t.Title
		score := 0
		for i, n := range needles {
			if n != "" && strings.Contains(hay, n) {
				score += 100 - i*10
			}
		}
		if strings.Contains(strings.ToLower(t.URL), "tradingview.com/chart") {
			score += 20
		}
		if score > 0 && score > bestScore {
			bestScore = score
			best = &t
		}
	}
	return best
}
