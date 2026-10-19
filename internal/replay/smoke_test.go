package replay

// 对着**正在运行的服务**做冒烟测试。默认跳过，只有设了 REPLAY_WS 才跑：
//
//	go run .            # 另开一个窗口，菜单选 4
//	REPLAY_WS=ws://127.0.0.1:8790/ws go test ./internal/replay/ -run TestSmoke -v
//
// 它和单元测试的区别是：用的是**真实数据目录**（data/ 里那些 CSV）、真实端口、
// 真实进程。凡是被合成测试数据掩盖的问题（真实标的名、真实时间范围、大文件加载）
// 都在这一条里暴露。画布渲染不在这里覆盖 —— 那需要浏览器，本仓库不使用无头浏览器。

import (
	"os"
	"testing"
	"time"

	"tradingview-j/internal/wsclient"
)

func TestSmokeAgainstRunningServer(t *testing.T) {
	url := os.Getenv("REPLAY_WS")
	if url == "" {
		t.Skip("设 REPLAY_WS=ws://127.0.0.1:8790/ws 可对正在运行的服务做冒烟测试")
	}
	conn, err := wsclient.Dial(url, wsclient.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("连接 %s 失败：%v", url, err)
	}
	defer conn.Close()
	if w := readMsg(t, conn); w["type"] != MsgWelcome {
		t.Fatalf("没有收到 welcome：%v", w)
	}

	// 1. 真实数据目录里的标的
	send(t, conn, map[string]any{"type": "list_symbols"})
	symMsg := readMsg(t, conn)
	symbols, _ := symMsg["symbols"].([]any)
	if len(symbols) == 0 {
		t.Fatalf("数据目录里没有标的：%v", symMsg)
	}
	first := symbols[0].(map[string]any)
	symbol, _ := first["symbol"].(string)
	t.Logf("第一个标的：%v（%v 天，%v ~ %v）", symbol, first["days"], first["firstDate"], first["lastDate"])

	// 2. 随机时间点建实例
	send(t, conn, map[string]any{"type": "create_instance", "symbol": symbol, "bars": 120})
	state := readMsg(t, conn)
	if state["type"] != MsgInstanceState {
		t.Fatalf("建实例失败：%v", state)
	}
	inst := state["instance"].(map[string]any)
	id := inst["id"].(string)
	bars := inst["bars"].([]any)
	if len(bars) != int(inst["cursor"].(float64))+1 {
		t.Fatalf("快照条数与游标不一致：bars=%d cursor=%v", len(bars), inst["cursor"])
	}
	last := bars[len(bars)-1].(map[string]any)
	t.Logf("实例 %s：%v，%d 根已揭示（锚点 %v），隐藏 %v 根",
		id, inst["symbol"], len(bars), inst["anchor"], inst["hiddenBars"])

	// 3. 步进
	send(t, conn, map[string]any{"type": "step", "instanceId": id})
	ev := readAction(t, conn, ActBarAdvanced)
	payload := ev["payload"].(map[string]any)
	if payload["bar"] == nil {
		t.Fatalf("步进广播缺少 bar：%v", payload)
	}

	// 4. 标注（吸附到最后一根的 OHLC）
	send(t, conn, map[string]any{
		"type":       "annotation_add",
		"instanceId": id,
		"kind":       "rect",
		"start":      map[string]any{"time": last["time"], "price": last["low"]},
		"end":        map[string]any{"time": last["time"], "price": last["high"]},
		"snap":       true,
	})
	annEv := readAction(t, conn, ActAnnotationAdded)
	ann := annEv["payload"].(map[string]any)["annotation"].(map[string]any)
	t.Logf("标注已加：%v %v", ann["id"], ann["kind"])

	// 5. 挂单 + 再步进
	send(t, conn, map[string]any{
		"type":       "order_place",
		"instanceId": id,
		"side":       "buy",
		"kind":       "limit",
		"price":      last["low"],
		"quantity":   1,
	})
	readAction(t, conn, ActOrderPlaced)

	// 5.5 市价单：不带价格，按当前收盘价立刻成交（一次广播里带订单/成交/持仓/盈亏）
	send(t, conn, map[string]any{
		"type":       "order_market",
		"instanceId": id,
		"side":       "buy",
		"quantity":   1,
	})
	filled := readAction(t, conn, ActOrderFilled)
	fp := filled["payload"].(map[string]any)
	if fp["order"].(map[string]any)["status"] != OrderFilled {
		t.Fatalf("市价单应当立刻成交：%v", fp["order"])
	}
	if pos := fp["position"].(map[string]any); pos["side"] == "flat" {
		t.Fatalf("市价买之后持仓不该还是空仓：%v", pos)
	}

	// 6. 机器人跑一下，确认能接管并产生动作
	send(t, conn, map[string]any{"type": "bot_start", "instanceId": id, "intervalMs": 200})
	readAction(t, conn, ActBotStarted)
	deadline := time.Now().Add(8 * time.Second)
	sawBot := false
	for time.Now().Before(deadline) && !sawBot {
		m := readMsg(t, conn)
		if m["type"] == MsgEvent && m["by"] == "bot" {
			sawBot = true
		}
	}
	if !sawBot {
		t.Fatal("机器人启动后没有产生任何动作")
	}
	send(t, conn, map[string]any{"type": "bot_stop", "instanceId": id})
	readAction(t, conn, ActBotStopped)
	t.Log("冒烟通过：标的、窗口、步进、标注、挂单、市价、机器人")
}
