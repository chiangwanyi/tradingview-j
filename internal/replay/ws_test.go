package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
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
	for _, k := range []string{"time", "open", "high", "low", "close", "volume", "ema20"} {
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

// 端点越过最后一根时，offset 必须原样走完「消息 -> 实例 -> 广播」这一圈。
//
// 这条用**JSON 数字字段**发（而不是 Go 结构体），就是为了顺带钉住协议里的字段名：
// 前端读的是 `end.offset`，改名后 Go 单测全绿、界面上却永远延伸不出去。
func TestAnnotationOffsetSurvivesWire(t *testing.T) {
	hub, root := newTestHub(t, 5)
	srv := httptest.NewServer(NewHandler(hub, root))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	c, err := wsclient.Dial(wsURL, wsclient.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	defer c.Close()
	readMsg(t, c) // welcome

	send(t, c, map[string]any{
		"type":   "create_instance",
		"symbol": testSymbol,
		"time":   "2026-01-06 10:00",
		"bars":   30,
	})
	state := readMsg(t, c)
	if state["type"] != MsgInstanceState {
		t.Fatalf("建实例后应当收到全量快照，实际 %v", state["type"])
	}
	inst := state["instance"].(map[string]any)
	id := inst["id"].(string)
	bars := inst["bars"].([]any)
	last := bars[len(bars)-1].(map[string]any)["time"].(string)

	send(t, c, map[string]any{
		"type":          "annotation_add",
		"instanceId":    id,
		"kind":          "trend",
		"start":         map[string]any{"time": last, "price": 100},
		"end":           map[string]any{"time": last, "price": 101.5, "offset": 40},
		"snap":          false,
	})
	ev := readAction(t, c, ActAnnotationAdded)
	payload := ev["payload"].(map[string]any)
	ann := payload["annotation"].(map[string]any)
	end := ann["end"].(map[string]any)
	if end["offset"] != float64(40) {
		t.Fatalf("广播里的 offset 应当是 40（字段名/透传被改了？），实际 %v", end["offset"])
	}
	if end["time"] != last {
		t.Fatalf("锚点必须是最后一根 K 线 %q，实际 %v", last, end["time"])
	}
	if start := ann["start"].(map[string]any); start["offset"] != nil {
		t.Fatalf("offset 为 0 时不该出现在 JSON 里：%v", start["offset"])
	}
}

// 这条盯的是「长时间空闲的连接会不会被自己踢掉」。
//
// 现场症状：用户打开页面就不动，日志里每 90 秒出现一次
// 「已连接 -> 已断开」，名字从 human2 一路涨到 human201。
// 根因是读超时只在进入一轮 ReadMessage 时设一次，而 pong 是在 ReadMessage
// 内部被消费的 —— 空闲连接永远走不完那一轮，90 秒后必定超时。
//
// 这里不去真的等 90 秒，而是数「收一帧有没有续一次读超时」：
// 喂两帧 pong（客户端帧必须带掩码），第三次读会撞到 EOF，
// 于是预期正好续了 3 次。旧写法是 0 次。
func TestReadDeadlineRenewedPerFrame(t *testing.T) {
	maskedPong := []byte{0x80 | opPong, 0x80, 0x01, 0x02, 0x03, 0x04} // FIN|pong，有掩码，载荷 0 字节
	raw := append(append([]byte{}, maskedPong...), maskedPong...)

	nc := &deadlineConn{r: bytes.NewReader(raw)}
	c := &wsConn{conn: nc, br: bufio.NewReader(nc), maxPayload: maxFramePayload}

	_, _, err := c.ReadMessage()
	if !errors.Is(err, errWSClosed) {
		t.Fatalf("读到 EOF 应当返回 errWSClosed，实际 %v", err)
	}
	if nc.deadlines != 3 {
		t.Fatalf("两帧 pong 加一次 EOF 应当续 3 次读超时，实际 %d 次", nc.deadlines)
	}
}

// deadlineConn 只做两件事：从一个字节缓冲里读，并数续了几次读超时。
type deadlineConn struct {
	r         *bytes.Reader
	deadlines int
}

func (c *deadlineConn) Read(p []byte) (int, error)       { return c.r.Read(p) }
func (c *deadlineConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *deadlineConn) Close() error                     { return nil }
func (c *deadlineConn) LocalAddr() net.Addr              { return testAddr{} }
func (c *deadlineConn) RemoteAddr() net.Addr             { return testAddr{} }
func (c *deadlineConn) SetDeadline(time.Time) error      { return nil }
func (c *deadlineConn) SetWriteDeadline(time.Time) error { return nil }
func (c *deadlineConn) SetReadDeadline(time.Time) error  { c.deadlines++; return nil }

type testAddr struct{}

func (testAddr) Network() string { return "test" }
func (testAddr) String() string  { return "test" }

// 端到端版：真起服务、真连接、真空闲。
// 心跳压到毫秒级（90 秒等不起），空闲期间只有 ping/pong，之后连接必须还能收发。
// 旧逻辑在这一步必挂 —— 也正好复现用户看到的「挂着不动就掉线重连」。
func TestIdleConnectionSurvivesHeartbeat(t *testing.T) {
	oldIdle, oldPing := readIdleTimeout, pingInterval
	readIdleTimeout, pingInterval = 300*time.Millisecond, 100*time.Millisecond
	defer func() { readIdleTimeout, pingInterval = oldIdle, oldPing }()

	hub, root := newTestHub(t, 5)
	srv := httptest.NewServer(NewHandler(hub, root))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	c, err := wsclient.Dial(wsURL, wsclient.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	defer c.Close()

	// 后台持续读：浏览器平时就是这样，只有在读的时候才会回 pong。
	msgs := make(chan map[string]any, 32)
	go func() {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			_, payload, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(payload, &m)
			msgs <- m
		}
	}()

	select {
	case m := <-msgs:
		if m["type"] != MsgWelcome {
			t.Fatalf("第一条应当是 welcome：%v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没收到 welcome")
	}

	// 空闲 2 秒 ≈ 6.6 倍读超时，中间一条业务消息都没有
	time.Sleep(2 * time.Second)

	send(t, c, map[string]any{"type": "list_symbols"})
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-msgs:
			if m["type"] == MsgSymbols {
				return
			}
		case <-deadline:
			t.Fatal("空闲之后连接已经不响应了（心跳把空闲连接踢掉了）")
		}
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
