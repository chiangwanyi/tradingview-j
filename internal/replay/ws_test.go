package replay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tradingview-j/internal/wsclient"
)

// 这一条是真正的端到端：真 HTTP 服务 + 真 WebSocket 握手 + 真帧收发。
// 单元测试验证的是逻辑，这里验证的是「浏览器那套握手能不能对上」——
// 掩码方向、Accept 头、帧长度编码，任何一处写反都只在这一层暴露。
func TestWebSocketEndToEnd(t *testing.T) {
	hub, root := newTestHub(t, 5)
	srv := httptest.NewServer(NewHandler(hub, root))
	defer srv.Close()

	// 静态页面：这个测试目录里没有 dist，应当给一句「前端还没构建」的提示，而不是 404
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("取首页失败：%v", err)
	}
	body := make([]byte, 2048)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if !strings.Contains(string(body[:n]), "前端还没构建") {
		t.Fatalf("没有 dist 时应当给出提示页，实际：%s", string(body[:n]))
	}
	if resp, err := http.Get(srv.URL + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("健康检查失败：%v", err)
	} else {
		resp.Body.Close()
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	a, err := wsclient.Dial(wsURL, wsclient.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("连接 A 失败：%v", err)
	}
	defer a.Close()
	welcome := readMsg(t, a)
	if welcome["type"] != MsgWelcome || welcome["name"] != "human" {
		t.Fatalf("没有收到 welcome：%v", welcome)
	}

	send(t, a, map[string]any{
		"type":   "create_instance",
		"symbol": testSymbol,
		"time":   "2026-01-06 10:00",
		"bars":   30,
	})
	state := readMsg(t, a)
	if state["type"] != MsgInstanceState {
		t.Fatalf("建实例后应当收到全量快照，实际 %v", state["type"])
	}
	inst := state["instance"].(map[string]any)
	id := inst["id"].(string)
	bars := inst["bars"].([]any)
	if len(bars) != int(inst["cursor"].(float64))+1 {
		t.Fatalf("快照应当只含已揭示的 K 线：bars=%d cursor=%v", len(bars), inst["cursor"])
	}
	first := bars[0].(map[string]any)
	for _, k := range []string{"time", "open", "high", "low", "close", "volume", "ema20", "barCount"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("K 线缺少字段 %q：%v", k, first)
		}
	}

	b, err := wsclient.Dial(wsURL, wsclient.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("连接 B 失败：%v", err)
	}
	defer b.Close()
	if w := readMsg(t, b); w["name"] != "human2" {
		t.Fatalf("第二个连接应当叫 human2：%v", w)
	}
	send(t, b, map[string]any{"type": "join_instance", "instanceId": id})
	if st := readMsg(t, b); st["type"] != MsgInstanceState {
		t.Fatalf("加入后应当收到快照，实际 %v", st["type"])
	}

	// A 步进，B 应当收到广播（加入实例时已经先广播过 members_changed，这里跳过它）
	send(t, a, map[string]any{"type": "step", "instanceId": id})
	ev := readAction(t, b, ActBarAdvanced)
	if ev["instanceId"] != id {
		t.Fatalf("广播的实例不对：%v", ev["instanceId"])
	}
	payload := ev["payload"].(map[string]any)
	if payload["cursor"] == nil || payload["position"] == nil {
		t.Fatalf("bar_advanced 缺少字段：%v", payload)
	}
}

func readMsg(t *testing.T, c *wsclient.Conn) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	op, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("读消息失败：%v", err)
	}
	if op != wsclient.OpText {
		t.Fatalf("收到了非文本帧：%d", op)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("消息不是 JSON：%v", err)
	}
	return m
}

// readAction 一直读到某条指定动作的广播，跳过中间的其它消息。
func readAction(t *testing.T, c *wsclient.Conn, action string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsg(t, c)
		if m["type"] == MsgEvent && m["action"] == action {
			return m
		}
	}
	t.Fatalf("没等到动作 %s", action)
	return nil
}

func send(t *testing.T, c *wsclient.Conn, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if err := c.WriteText(b); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
}
