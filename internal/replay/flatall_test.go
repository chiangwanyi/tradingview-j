package replay

import (
	"strings"
	"testing"
)

// 「市价退仓并取消所有挂单」与「换图形样式」两条协议（v0.20.0）。
//
// 这两个功能各有一个**不能只靠前端保证**的点，所以必须在协议层钉住：
//   - 退仓：一条消息里既撤单又平仓，且空仓 + 无挂单时要给一句准话（不静默成功）；
//   - 换样式：同类约束由**图形自己的 kind** 决定，前端只列同类模板不算证据 ——
//     别的入口（脚本、将来的批量改）把斐波那契的水平色塞给趋势线，也不能画出怪东西。

// lastBarTime 取实例当前游标那根 K 线的时间（标注端点必须锚在已揭示的 K 线上）。
func lastBarTime(t *testing.T, hub *Hub, id string) string {
	t.Helper()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	inst, ok := hub.instances[id]
	if !ok {
		t.Fatalf("实例 %s 不存在", id)
	}
	return inst.Bars[inst.Cursor].Time
}

func TestFlatAllOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	op := newFakeClient(t, hub)
	op.waitFor(MsgWelcome)
	id := createInstance(t, hub, op, "2026-01-06 10:00")

	// 先市价买 2 手（有仓位），再挂一张限价单（有挂单）——
	// 一键退仓要能同时清掉这两样。
	op.send(hub, map[string]any{
		"type":       "order_market",
		"instanceId": id,
		"side":       "buy",
		"quantity":   2,
	})
	op.waitAction(ActOrderFilled)

	hub.mu.Lock()
	close0 := hub.instances[id].Bars[hub.instances[id].Cursor].Close
	hub.mu.Unlock()

	op.send(hub, map[string]any{
		"type":       "order_place",
		"instanceId": id,
		"side":       "buy",
		"kind":       "limit",
		"price":      close0 - 5,
		"quantity":   1,
	})
	op.waitAction(ActOrderPlaced)

	op.send(hub, map[string]any{"type": "order_flat_all", "instanceId": id})
	ev := op.waitAction(ActFlatAll)
	payload := ev["payload"].(map[string]any)

	pos := payload["position"].(map[string]any)
	if pos["side"] != "flat" || pos["quantity"].(float64) != 0 {
		t.Fatalf("退仓后应当是空仓，实际 %v", pos)
	}
	if payload["cancelled"].(float64) != 1 {
		t.Fatalf("应当撤销 1 张挂单，实际 %v", payload["cancelled"])
	}
	orders := payload["orders"].([]any)
	for _, o := range orders {
		if m := o.(map[string]any); m["status"] == OrderOpen {
			t.Fatalf("退仓后不该还有挂着的单：%v", m)
		}
	}

	trade := payload["trade"].(map[string]any)
	if trade["side"] != "sell" || trade["quantity"].(float64) != 2 {
		t.Fatalf("平掉 2 手多头应当是一笔卖 2 手的成交，实际 %v", trade)
	}
	// 平仓价就是现价（市价单口径），和手动「市价卖出」完全一致
	if trade["price"].(float64) != roundPrice(close0) {
		t.Fatalf("退仓的成交价应当是现价 %v，实际 %v", roundPrice(close0), trade["price"])
	}

	// 再来一次：既没仓位也没挂单 —— 必须报错，不能静默成功
	op.send(hub, map[string]any{"type": "order_flat_all", "instanceId": id})
	if e := op.waitFor(MsgError); !strings.Contains(e["message"].(string), "不用退仓") {
		t.Fatalf("空仓再退仓应当说明没事可做，实际 %v", e["message"])
	}
}

// 只剩挂单（空仓）时也要能用：撤单本身就是一个有意义的动作。
func TestFlatAllCancelsOrdersWhenFlat(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	op := newFakeClient(t, hub)
	op.waitFor(MsgWelcome)
	id := createInstance(t, hub, op, "2026-01-06 10:00")

	hub.mu.Lock()
	close0 := hub.instances[id].Bars[hub.instances[id].Cursor].Close
	hub.mu.Unlock()

	for i := 0; i < 2; i++ {
		op.send(hub, map[string]any{
			"type":       "order_place",
			"instanceId": id,
			"side":       "sell",
			"kind":       "stop",
			"price":      close0 - float64(i+1),
			"quantity":   1,
		})
		op.waitAction(ActOrderPlaced)
	}

	op.send(hub, map[string]any{"type": "order_flat_all", "instanceId": id})
	payload := op.waitAction(ActFlatAll)["payload"].(map[string]any)
	if payload["cancelled"].(float64) != 2 {
		t.Fatalf("应当撤销 2 张挂单，实际 %v", payload["cancelled"])
	}
	if _, ok := payload["trade"]; ok {
		t.Fatalf("本来空仓时不该有成交：%v", payload["trade"])
	}
	if pos := payload["position"].(map[string]any); pos["side"] != "flat" {
		t.Fatalf("空仓退仓之后仍然是空仓，实际 %v", pos)
	}
}

func TestAnnotationStyleOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	op := newFakeClient(t, hub)
	op.waitFor(MsgWelcome)
	id := createInstance(t, hub, op, "2026-01-06 10:00")
	last := lastBarTime(t, hub, id)

	op.send(hub, map[string]any{
		"type":       "annotation_add",
		"instanceId": id,
		"kind":       "trend",
		"start":      map[string]any{"time": last, "price": 100},
		"end":        map[string]any{"time": last, "price": 104},
		"style":      map[string]any{"color": "#0ea5e9", "width": 2, "dash": "solid"},
	})
	added := op.waitAction(ActAnnotationAdded)["payload"].(map[string]any)["annotation"].(map[string]any)
	annID := added["id"].(string)

	// 换样式：颜色 / 线宽 / 线型都跟着变；端点、编号、创建人**一个都不动**。
	// 另外塞一份斐波那契的 levels 进去 —— 趋势线不该因此长出四条水平线。
	op.send(hub, map[string]any{
		"type":         "annotation_style",
		"instanceId":   id,
		"annotationId": annID,
		"style": map[string]any{
			"color":  "#a855f7",
			"width":  3,
			"dash":   "dashed",
			"levels": []any{map[string]any{"ratio": 0, "color": "#ff0000"}},
		},
	})
	styled := op.waitAction(ActAnnotationStyled)["payload"].(map[string]any)["annotation"].(map[string]any)

	style := styled["style"].(map[string]any)
	if style["color"] != "#a855f7" {
		t.Fatalf("颜色应当换成 #a855f7，实际 %v", style["color"])
	}
	if style["width"].(float64) != 3 {
		t.Fatalf("线宽应当换成 3，实际 %v", style["width"])
	}
	if style["dash"] != "dashed" {
		t.Fatalf("线型应当换成 dashed，实际 %v", style["dash"])
	}
	if lv, ok := style["levels"]; ok && lv != nil {
		t.Fatalf("趋势线不该带斐波那契的水平色：%v", lv)
	}
	if styled["kind"] != "trend" || styled["createdBy"] != "human" {
		t.Fatalf("换样式不该改类型和创建人：%v", styled)
	}
	if styled["start"].(map[string]any)["price"].(float64) != 100 ||
		styled["end"].(map[string]any)["price"].(float64) != 104 {
		t.Fatalf("换样式不该动端点：%v → %v", styled["start"], styled["end"])
	}

	// 不存在的编号：给一句能看懂的话，不要静默
	op.send(hub, map[string]any{
		"type":         "annotation_style",
		"instanceId":   id,
		"annotationId": "a999",
		"style":        map[string]any{"color": "#22c55e", "width": 1},
	})
	if e := op.waitFor(MsgError); !strings.Contains(e["message"].(string), "没有这个标注") {
		t.Fatalf("改不存在的图形应当报错，实际 %v", e["message"])
	}

	// 观众改不了别人的图形（和拖动、删除同一条口径）
	viewer := newFakeClient(t, hub)
	viewer.waitFor(MsgWelcome)
	viewer.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	viewer.waitFor(MsgInstanceState)
	viewer.send(hub, map[string]any{
		"type":         "annotation_style",
		"instanceId":   id,
		"annotationId": annID,
		"style":        map[string]any{"color": "#22c55e", "width": 1},
	})
	if e := viewer.waitFor(MsgError); !strings.Contains(e["message"].(string), "只能观看") {
		t.Fatalf("观众换样式应当被拒绝，实际 %v", e["message"])
	}
}
