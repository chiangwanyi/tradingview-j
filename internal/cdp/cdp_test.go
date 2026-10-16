package cdp

import (
	"bufio"
	"context"
	"encoding/json"

	"net"
	"strings"
	"testing"
	"time"

	"tradingview-j/internal/wsclient"
	"tradingview-j/internal/wstest"
)

func TestParseWSPath(t *testing.T) {
	cases := map[string]string{
		"ws://127.0.0.1:9222/devtools/browser/26fdce33-aa": "/devtools/browser/26fdce33-aa",
		"/devtools/browser/abc":                            "/devtools/browser/abc",
		"ws://127.0.0.1:9222/devtools/browser/abc/":        "/devtools/browser/abc",
		"":                                   "",
		"http://127.0.0.1:9222/json/version": "",
	}
	for in, want := range cases {
		if got := parseWSPath(in); got != want {
			t.Fatalf("parseWSPath(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestMatchTargetPrefersChartPage(t *testing.T) {
	targets := []Target{
		{ID: "1", Type: "page", URL: "https://www.google.com/", Title: "Google"},
		{ID: "2", Type: "page", URL: "https://cn.tradingview.com/pricing/", Title: "Pricing"},
		{ID: "3", Type: "page", URL: "https://cn.tradingview.com/chart/EuR5QG29/?symbol=CME_MINI%3AMNQ1%21", Title: "MNQ1!"},
		{ID: "4", Type: "service_worker", URL: "https://cn.tradingview.com/chart/x/", Title: "worker"},
	}
	got := MatchTarget(targets, []string{"tradingview.com/chart"})
	if got == nil || got.ID != "3" {
		t.Fatalf("挑中的页面 = %+v，期望 id=3", got)
	}
	if MatchTarget(targets[:1], []string{"tradingview.com/chart"}) != nil {
		t.Fatal("没有匹配页面时应返回 nil")
	}
}

func TestNotFoundErrorMessageIsActionable(t *testing.T) {
	err := &NotFoundError{
		Candidates: []string{"端口 9222（端口探测）：无监听"},
		PortFiles:  []PortFile{{Path: `C:\x\DevToolsActivePort`, Port: 0, Err: "拒绝访问"}},
	}
	msg := err.Error()
	for _, want := range []string{"9222", "DevToolsActivePort", "拒绝访问"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
}

// fakeCDP 扮演一个只会应答固定命令的 CDP 服务端。
// 目的不是模拟 Chrome，而是把「命令 id 匹配 / 事件分发 / sessionId 路由」这三件事测出来。
func fakeCDP(t *testing.T) *wstest.Server {
	t.Helper()
	return wstest.Start(t, func(conn net.Conn, br *bufio.Reader) error {
		// 握手后先推两条事件：一条是浏览器级的，一条是页面会话的
		push := func(v any) error {
			buf, _ := json.Marshal(v)
			return wstest.WriteText(conn, string(buf))
		}
		if err := push(map[string]any{
			"method": "Target.targetCreated",
			"params": map[string]any{"targetInfo": map[string]any{"targetId": "T1", "type": "page"}},
		}); err != nil {
			return err
		}
		if err := push(map[string]any{
			"sessionId": "S1",
			"method":    "Network.webSocketFrameReceived",
			"params":    map[string]any{"requestId": "1", "response": map[string]any{"opcode": 1, "payloadData": "~m~4~m~~h~1"}},
		}); err != nil {
			return err
		}

		for {
			op, payload, err := wstest.ReadFrame(br)
			if err != nil {
				return nil
			}
			if op != wstest.OpText {
				continue
			}
			var req struct {
				ID        int64           `json:"id"`
				Method    string          `json:"method"`
				SessionID string          `json:"sessionId"`
				Params    json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(payload, &req); err != nil {
				return err
			}
			var result any
			switch req.Method {
			case "Target.getTargets":
				result = map[string]any{"targetInfos": []map[string]any{
					{"targetId": "T1", "type": "page", "title": "MNQ1!", "url": "https://cn.tradingview.com/chart/x/?symbol=CME_MINI%3AMNQ1%21"},
					{"targetId": "T2", "type": "page", "title": "别的", "url": "https://example.com/"},
				}}
			case "Target.attachToTarget":
				result = map[string]any{"sessionId": "S1"}
			case "Network.enable":
				result = map[string]any{}
			case "Target.detachFromTarget":
				result = map[string]any{}
			case "Boom.command":
				// 故意返回错误，验证 Call 能把 CDP 错误原样带出来
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"},
				})
				if err := wstest.WriteText(conn, string(resp)); err != nil {
					return err
				}
				continue
			case "Never.answers":
				continue // 什么都不回，用来测超时
			default:
				result = map[string]any{}
			}
			resp, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
			if err := wstest.WriteText(conn, string(resp)); err != nil {
				return err
			}
		}
	})
}

func dialFake(t *testing.T, ts *wstest.Server) *Transport {
	t.Helper()
	tr, err := DialTransport(ts.URL, wsclient.Options{Timeout: 3 * time.Second, MaxPayload: 8 << 20})
	if err != nil {
		t.Fatalf("DialTransport: %v", err)
	}
	t.Cleanup(tr.Close)
	return tr
}

func TestTransportCallsAndEvents(t *testing.T) {
	ts := fakeCDP(t)
	tr := dialFake(t, ts)

	type event = cdpTestEvent
	events := make(chan cdpTestEvent, 8)
	tr.OnEvent(func(session, method string, params json.RawMessage) {
		events <- event{session: session, method: method, params: params}
	})

	ctx := context.Background()

	// 事件先到：会话号要能对上（页面级事件带 sessionId，浏览器级不带）
	got := waitEvent(t, events)
	if got.method != "Target.targetCreated" || got.session != "" {
		t.Fatalf("浏览器级事件不对: %+v", got)
	}
	got = waitEvent(t, events)
	if got.method != "Network.webSocketFrameReceived" || got.session != "S1" {
		t.Fatalf("页面级事件不对: %+v", got)
	}
	if !strings.Contains(string(got.params), "~h~1") {
		t.Fatalf("事件参数没透传: %s", got.params)
	}

	targets, err := tr.GetTargets(ctx)
	if err != nil {
		t.Fatalf("GetTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d", len(targets))
	}
	if best := MatchTarget(targets, []string{"tradingview.com/chart"}); best == nil || best.ID != "T1" {
		t.Fatalf("挑中的页面 = %+v", best)
	}

	sid, err := tr.Attach(ctx, "T1")
	if err != nil || sid != "S1" {
		t.Fatalf("Attach = %q, %v", sid, err)
	}
	if err := tr.EnableNetwork(ctx, sid, 200); err != nil {
		t.Fatalf("EnableNetwork: %v", err)
	}

	// CDP 返回的错误要能带出来，而不是被吞掉
	if _, err := tr.Call(ctx, "", "Boom.command", nil); err == nil {
		t.Fatal("CDP 错误没有被上报")
	} else if !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("错误信息不完整: %v", err)
	}
}

func TestTransportTimesOutOnSilentPeer(t *testing.T) {
	ts := fakeCDP(t)
	tr := dialFake(t, ts)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := tr.Call(ctx, "", "Never.answers", nil); err == nil {
		t.Fatal("对端不回应时应当超时返回，而不是永久阻塞")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时用了 %v，太久", elapsed)
	}
}

func TestTransportReportsClosure(t *testing.T) {
	ts := wstest.Start(t, func(conn net.Conn, br *bufio.Reader) error {
		// 直接断开，模拟用户在 Chrome 里关掉远程调试开关
		return wstest.WriteClose(conn)
	})
	tr := dialFake(t, ts)

	select {
	case <-tr.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("对端断开后 Done 没有关闭")
	}
	if _, err := tr.Call(context.Background(), "", "Target.getTargets", nil); err == nil {
		t.Fatal("连接关掉之后 Call 应当立刻报错")
	}
}

type cdpTestEvent struct {
	session string
	method  string
	params  json.RawMessage
}

func waitEvent(t *testing.T, ch chan cdpTestEvent) cdpTestEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("等事件超时")
	}
	return cdpTestEvent{}
}
