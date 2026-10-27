package replay

import (
	"math/rand"
	"testing"
	"time"
)

// createInstance 用假客户端建一个实例，返回实例 id。
func createInstance(t *testing.T, hub *Hub, c *fakeClient, anchor string) string {
	t.Helper()
	payload := map[string]any{
		"type":   "create_instance",
		"symbol": testSymbol,
		"time":   anchor,
		"bars":   40,
	}
	c.send(hub, payload)
	st := c.waitFor(MsgInstanceState)
	inst := st["instance"].(map[string]any)
	id, _ := inst["id"].(string)
	if id == "" {
		t.Fatalf("创建实例没有返回 id：%v", st)
	}
	return id
}

// 名字只在「同时在线」时唯一：人走了要还回池子。
// 不还的话，掉线重连（或刷新页面）会让名字一路涨到 human201 —— 这不是计数错了，
// 而是上面那条 90 秒心跳超时的副作用被放大后的样子。
func TestDisconnectReleasesName(t *testing.T) {
	hub, _ := newTestHub(t, 5)

	a := newFakeClient(t, hub)
	if w := a.waitFor(MsgWelcome); w["name"] != "human" {
		t.Fatalf("第一个连接应当叫 human，实际 %v", w["name"])
	}
	hub.Unregister(a.conn)

	b := newFakeClient(t, hub)
	if w := b.waitFor(MsgWelcome); w["name"] != "human" {
		t.Fatalf("上一个连接断开后名字应当可以再用，实际 %v", w["name"])
	}

	// 但同时在线时仍然必须唯一，否则广播里分不清是谁干的
	c := newFakeClient(t, hub)
	if w := c.waitFor(MsgWelcome); w["name"] != "human2" {
		t.Fatalf("同时在线时第二个连接应当叫 human2，实际 %v", w["name"])
	}
}

func TestCreateJoinAndOperator(t *testing.T) {
	hub, _ := newTestHub(t, 5)

	a := newFakeClient(t, hub)
	if w := a.waitFor(MsgWelcome); w["name"] != "human" {
		t.Fatalf("第一个连接应当叫 human，实际 %v", w["name"])
	}

	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	if w := b.waitFor(MsgWelcome); w["name"] != "human2" {
		t.Fatalf("第二个连接应当叫 human2，实际 %v", w["name"])
	}
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	bst := b.waitFor(MsgInstanceState)
	you := bst["you"].(map[string]any)
	if you["operator"] == true {
		t.Fatal("后进来的人不该拿到操作权")
	}
	inst := bst["instance"].(map[string]any)
	if inst["operator"] != "human" {
		t.Fatalf("操作者应当是 human，实际 %v", inst["operator"])
	}
	if len(inst["members"].([]any)) != 2 {
		t.Fatalf("成员应当是 2 人，实际 %d", len(inst["members"].([]any)))
	}

	// 观看者不能操作
	b.drain()
	b.send(hub, map[string]any{"type": "step", "instanceId": id})
	if e := b.waitFor(MsgError); e["message"] == "" {
		t.Fatal("观看者步进应当收到错误")
	}

	// 操作者步进，观看者收到广播
	b.drain()
	a.send(hub, map[string]any{"type": "step", "instanceId": id})
	ev := b.waitFor(MsgEvent)
	if ev["action"] != ActBarAdvanced || ev["by"] != "human" {
		t.Fatalf("广播内容不对：%v", ev)
	}
	if ev["instanceId"] != id {
		t.Fatalf("广播的实例不对：%v", ev["instanceId"])
	}
}

// 【新增实例（最新）】走 create_instance 的 latest 开关：锚点由后端取数据目录里最新的
// 那一根，请求里带了 time 也一律忽略（前端的「最新」按钮就是这么发的）。
func TestCreateLatestInstanceAnchorsAtNewestBar(t *testing.T) {
	hub, _ := newTestHub(t, 5) // 01-05 ~ 01-09，每天 09:00 ~ 11:00
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	a.send(hub, map[string]any{
		"type": "create_instance", "symbol": testSymbol,
		"latest": true, "time": "2026-01-06 10:00", "bars": 40, "forwardDays": 1,
	})
	st := a.waitFor(MsgInstanceState)
	inst := st["instance"].(map[string]any)

	if want := "2026-01-09 11:00"; inst["anchor"] != want {
		t.Fatalf("最新模式的锚点应当是 %s，实际 %v", want, inst["anchor"])
	}
	bars, _ := inst["bars"].([]any)
	if len(bars) == 0 {
		t.Fatal("实例没有 K 线")
	}
	// 快照只含已揭示的 K 线，最后一根就是锚点
	if last := bars[len(bars)-1].(map[string]any)["time"]; last != inst["anchor"] {
		t.Fatalf("快照最后一根应当是锚点 %v，实际 %v", inst["anchor"], last)
	}
	if inst["hiddenBars"] != float64(0) || inst["hasMore"] != false {
		t.Fatalf("最新模式后面不该还有行情：hiddenBars=%v hasMore=%v", inst["hiddenBars"], inst["hasMore"])
	}
}

func TestInstancesAreIsolated(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	idA := createInstance(t, hub, a, "2026-01-05 10:00")

	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)
	idC := createInstance(t, hub, c, "2026-01-07 10:00")
	if idA == idC {
		t.Fatalf("两个实例的 id 不该相同：%s", idA)
	}

	a.drain()
	c.drain()
	c.send(hub, map[string]any{"type": "step", "instanceId": idC})
	if ev := c.waitFor(MsgEvent); ev["instanceId"] != idC {
		t.Fatalf("C 应当收到自己实例的广播，实际 %v", ev["instanceId"])
	}

	// A 在另一个实例里，绝不能收到 C 的数据
	time.Sleep(120 * time.Millisecond)
	if a.hasType(MsgEvent) {
		t.Fatal("A 收到了别的实例的广播")
	}
}

func TestLeaveAndTakeControl(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	b.waitFor(MsgInstanceState)

	// A 交还操作权
	a.send(hub, map[string]any{"type": "release_control", "instanceId": id})
	if ev := a.waitAction(ActControlChanged); evPayloadString(ev, "operator") != "" {
		t.Fatalf("操作权应当已交还，实际 %q", evPayloadString(ev, "operator"))
	}
	b.drain() // 上面那条 control_changed 也广播给了 B，先清掉

	// B 接管
	b.send(hub, map[string]any{"type": "take_control", "instanceId": id})
	if ev := b.waitAction(ActControlChanged); evPayloadString(ev, "operator") != "human2" {
		t.Fatalf("B 接管失败：%v", ev)
	}
	b.send(hub, map[string]any{"type": "step", "instanceId": id})
	b.waitAction(ActBarAdvanced)

	// A 退出后，B 刷新快照只看到 1 个人
	a.send(hub, map[string]any{"type": "leave_instance", "instanceId": id})
	if m := a.waitFor(MsgLeft); m["instanceId"] != id {
		t.Fatalf("退出回执不对：%v", m)
	}
	b.send(hub, map[string]any{"type": "take_control", "instanceId": id}) // 已经是自己，回报错误
	b.waitFor(MsgError)
}

func TestDeleteRequiresCreatorOrOperator(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	b.waitFor(MsgInstanceState)

	// B 只是观众（A 还是操作者），不能删
	b.send(hub, map[string]any{"type": "delete_instance", "instanceId": id})
	if e := b.waitFor(MsgError); e["message"] == "" {
		t.Fatal("观众删实例应当被拒绝")
	}

	a.send(hub, map[string]any{"type": "delete_instance", "instanceId": id})
	if m := a.waitFor(MsgDeleted); m["instanceId"] != id {
		t.Fatalf("删除回执不对：%v", m)
	}
	if m := b.waitFor(MsgDeleted); m["instanceId"] != id {
		t.Fatalf("实例里其他人也该收到删除通知，实际 %v", m)
	}
	// 删完之后再操作应当报「不存在」
	a.send(hub, map[string]any{"type": "step", "instanceId": id})
	if e := a.waitFor(MsgError); e["message"] == "" {
		t.Fatal("删除后步进应当报错")
	}
}

// 删掉一个实例、紧接着新建一个：新实例身上**不许再出现删除通知**。
//
// 前端那句「这个实例已经被删除了」的误报就是这么来的：删除回执和新建实例的快照
// 只隔一两条消息，界面把旧 id 的回执当成了「当前这块盘面没了」，于是新建完之后
// 提示还在（旧版本里它是一条不会自己消失的错误）。协议这一侧钉死两件事：
// 回执只指向被删的那个 id、新实例复用不了旧 id，前端才有条件判断「这条跟我有关吗」。
func TestDeleteThenCreateKeepsNewInstanceClean(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	oldID := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{"type": "delete_instance", "instanceId": oldID})
	if m := a.waitFor(MsgDeleted); m["instanceId"] != oldID {
		t.Fatalf("删除回执应当指向被删的实例 %s，实际 %v", oldID, m)
	}
	a.drain()

	newID := createInstance(t, hub, a, "2026-01-06 10:30")
	if newID == oldID {
		t.Fatalf("新建的实例不该复用被删的 id %s", oldID)
	}

drain:
	for {
		select {
		case m := <-a.msg:
			if m["type"] == MsgDeleted {
				t.Fatalf("新建实例 %s 之后不该再收到删除通知：%v", newID, m)
			}
		default:
			break drain
		}
	}
}

// 市价单走的是 order_market：一次广播里要带全「订单 + 成交 + 持仓 + 盈亏」，
// 前端收到就能直接改界面，不用再要快照。
func TestMarketOrderOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{
		"type":       "order_market",
		"instanceId": id,
		"side":       "buy",
		"quantity":   2,
	})
	ev := a.waitAction(ActOrderFilled)

	payload := ev["payload"].(map[string]any)
	order := payload["order"].(map[string]any)
	if order["kind"] != KindMarket || order["status"] != OrderFilled {
		t.Fatalf("市价单应当是 market/filled，实际 %v/%v", order["kind"], order["status"])
	}
	if order["fillPrice"] == nil || order["fillPrice"] == 0.0 {
		t.Fatalf("市价单必须带成交价：%v", order)
	}
	if trades := payload["trades"].([]any); len(trades) != 1 {
		t.Fatalf("应当有 1 笔成交，实际 %d", len(trades))
	}
	pos := payload["position"].(map[string]any)
	if pos["side"] != "long" || pos["quantity"].(float64) != 2 {
		t.Fatalf("市价买 2 手后应当是多 2：%v", pos)
	}
	if _, ok := payload["unrealizedPnl"]; !ok {
		t.Fatalf("广播里应当带浮动盈亏：%v", payload)
	}
}

// nATR 定手数：带 stopDistance 的 order_market 要**一次广播**里同时出现
// 「成交」和「那张止损单」—— 中间态（有仓、没止损）绝不能漏给客户端。
func TestMarketOrderWithStopOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{
		"type":         "order_market",
		"instanceId":   id,
		"side":         "buy",
		"quantity":     2,
		"stopDistance": 4,
	})
	ev := a.waitAction(ActOrderFilled)
	payload := ev["payload"].(map[string]any)

	order := payload["order"].(map[string]any)
	fill := order["fillPrice"].(float64)
	stop, ok := payload["stopOrder"].(map[string]any)
	if !ok {
		t.Fatalf("广播里应当带 stopOrder：%v", payload)
	}
	if stop["side"] != "sell" || stop["kind"] != KindStop || stop["status"] != OrderOpen {
		t.Fatalf("保护单应当是 open 的 sell/stop：%v", stop)
	}
	if want := fill - 4; stop["price"].(float64) != want {
		t.Fatalf("止损价应当是成交价-4=%v，实际 %v", want, stop["price"])
	}
	if stop["quantity"].(float64) != 2 {
		t.Fatalf("止损手数应当和开仓一致：%v", stop["quantity"])
	}
	// 同一份 payload 的 orders 里也要有它（前端整份替换挂单列表靠这个）
	orders := payload["orders"].([]any)
	if len(orders) != 2 {
		t.Fatalf("应当有市价单 + 止损单两张，实际 %d", len(orders))
	}
}

// 观看者不能下市价单：它和挂单一样要操作权。
func TestMarketOrderRequiresOperator(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	b.waitFor(MsgInstanceState)

	b.send(hub, map[string]any{
		"type":       "order_market",
		"instanceId": id,
		"side":       "buy",
		"quantity":   1,
	})
	if e := b.waitFor(MsgError); e["message"] == "" {
		t.Fatal("没有操作权下市价单应当收到错误")
	}
}

func TestBotsCanStepAnnotateAndTrade(t *testing.T) {
	hub, root := newTestHub(t, 6)
	inst := openTestInstance(t, root, "2026-01-06 10:00", 40, 2)
	inst.ID = "ins-bot"
	hub.instances[inst.ID] = inst
	hub.order = append(hub.order, inst.ID)
	inst.botStop = make(chan struct{}) // 假装机器人已启动，直接驱动 tick
	inst.Operator = "bot"
	hub.rnd = rand.New(rand.NewSource(7))

	startCursor := inst.Cursor
	for i := 0; i < 300; i++ {
		hub.botTickLocked(inst)
	}

	if inst.Cursor <= startCursor {
		t.Fatal("机器人没有推进 K 线")
	}
	if inst.seqAnn == 0 {
		t.Fatal("机器人没有画过标注")
	}
	if inst.seqOrder == 0 {
		t.Fatal("机器人没有下过单")
	}
	if len(inst.Annotations) > botMaxAnnotations {
		t.Fatalf("同时存在的标注数量没有被限制：%d", len(inst.Annotations))
	}

	var stepped, annotated, ordered bool
	for _, ev := range inst.Events {
		if ev.By != "bot" {
			t.Fatalf("机器人事件的 by 应当是 bot，实际 %q", ev.By)
		}
		switch ev.Action {
		case ActBarAdvanced:
			stepped = true
		case ActAnnotationAdded, ActAnnotationMoved:
			annotated = true
		case ActOrderPlaced, ActOrderCancelled:
			ordered = true
		}
	}
	if !stepped || !annotated || !ordered {
		t.Fatalf("机器人应当覆盖步进/标注/交易三类操作：step=%v annotation=%v order=%v", stepped, annotated, ordered)
	}
	if inst.Operator != "bot" {
		t.Fatalf("机器人应当持有操作权，实际 %q", inst.Operator)
	}
}

func TestBotStartStopOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 6)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{"type": "bot_start", "instanceId": id, "intervalMs": 150})
	started := a.waitAction(ActBotStarted)
	if started["by"] != "human" {
		t.Fatalf("启动动作应当记在发起人身上，实际 %v", started["by"])
	}

	// 等到机器人至少做出一次带 by=bot 的动作
	deadline := time.After(6 * time.Second)
	sawBot := false
	for !sawBot {
		select {
		case m := <-a.msg:
			if m["type"] == MsgEvent && m["by"] == "bot" {
				sawBot = true
			}
		case <-deadline:
			t.Fatal("机器人启动后没有任何动作")
		}
	}

	a.send(hub, map[string]any{"type": "bot_stop", "instanceId": id})
	stopped := a.waitFor(MsgEvent)
	if stopped["action"] != ActBotStopped {
		t.Fatalf("应当广播机器人停止，实际 %v", stopped["action"])
	}
	// 停止后操作权交回叫停的人
	if ev := a.waitFor(MsgEvent); ev["action"] != ActControlChanged || evPayloadString(ev, "operator") != "human" {
		t.Fatalf("停止机器人后操作权应当交回 human：%v", ev)
	}
}

func TestBotCannotStartWhileSomeoneElseOperates(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": id})
	b.waitFor(MsgInstanceState)

	b.send(hub, map[string]any{"type": "bot_start", "instanceId": id})
	if e := b.waitFor(MsgError); e["message"] == "" {
		t.Fatal("别人在操作时机器人不该起来")
	}
}

func TestOperationsRequireMembership(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	// B 没 join，直接拿 id 去操作
	b.send(hub, map[string]any{"type": "step", "instanceId": id})
	if e := b.waitFor(MsgError); e["message"] == "" {
		t.Fatal("没进入实例就操作应当报错")
	}
}

func evPayloadString(ev map[string]any, key string) string {
	p, ok := ev["payload"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := p[key].(string)
	return s
}

// 网页上点【下一根 →】走的就是这条路：create_instance -> step -> bar_advanced 广播。
// 所以这里不去看实例内部字段，而是从广播里取 bar.time —— 和界面显示的是同一个值。
//
// 0.5.1 修的 bug 在这条测试里表现为：锚点 09:30，点下一根拿到的是第二天的 09:00。
func TestStepRequestReturnsAdjacentNextBar(t *testing.T) {
	hub, root := newTestHub(t, 5) // 每天 09:00 ~ 11:00，25 根
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	cases := []struct {
		name string
		req  map[string]any
	}{
		{"指定时间加载", map[string]any{
			"type": "create_instance", "symbol": testSymbol, "time": "2026-01-06 09:30", "bars": 40, "forwardDays": 1,
		}},
		{"随机加载（种子 7）", map[string]any{
			"type": "create_instance", "symbol": testSymbol, "time": "", "bars": 40, "forwardDays": 1, "seed": 7,
		}},
		{"随机加载（种子 21）", map[string]any{
			"type": "create_instance", "symbol": testSymbol, "time": "", "bars": 40, "forwardDays": 1, "seed": 21,
		}},
	}
	for _, tc := range cases {
		a.drain()
		a.send(hub, tc.req)
		st := a.waitFor(MsgInstanceState)
		inst := st["instance"].(map[string]any)
		id, _ := inst["id"].(string)
		anchor, _ := inst["anchor"].(string)
		bars, _ := inst["bars"].([]any)
		if id == "" || anchor == "" || len(bars) == 0 {
			t.Fatalf("%s：实例状态不完整：%v", tc.name, inst)
		}
		// 快照只含已揭示的 K 线，最后一根就是锚点
		last, _ := bars[len(bars)-1].(map[string]any)["time"].(string)
		if last != anchor {
			t.Fatalf("%s：快照最后一根应当是锚点 %s，实际 %s", tc.name, anchor, last)
		}

		want, ok := expectedNextBar(t, root, testFolder, anchor)
		if !ok {
			t.Fatalf("%s：锚点 %s 之后应当还有 K 线", tc.name, anchor)
		}
		a.send(hub, map[string]any{"type": "step", "instanceId": id})
		ev := a.waitAction(ActBarAdvanced)
		payload, ok := ev["payload"].(map[string]any)
		if !ok {
			t.Fatalf("%s：bar_advanced 没有 payload：%v", tc.name, ev)
		}
		bar, ok := payload["bar"].(map[string]any)
		if !ok {
			t.Fatalf("%s：payload 里没有 bar：%v", tc.name, payload)
		}
		if got, _ := bar["time"].(string); got != want {
			t.Fatalf("%s：锚点 %s 点【下一根】应当返回 %s，实际返回 %s", tc.name, anchor, want, got)
		}
	}
}

// 拖动挂单线走的是 order_modify：广播里必须带「改完的那一张」和全量列表 ——
// 前者给操作记录写清改的是谁、改成了多少，后者让所有人（含观众）看到同一个盘面。
func TestMoveOrderOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{
		"type":       "order_place",
		"instanceId": id,
		"side":       "buy",
		"kind":       KindLimit,
		"price":      99.0,
		"quantity":   2,
	})
	placed := a.waitAction(ActOrderPlaced)
	orders := placed["payload"].(map[string]any)["orders"].([]any)
	orderID, _ := orders[0].(map[string]any)["id"].(string)
	if orderID == "" {
		t.Fatalf("挂单广播里没有订单 id：%v", placed)
	}

	a.send(hub, map[string]any{
		"type":       "order_modify",
		"instanceId": id,
		"orderId":    orderID,
		"price":      97.5,
	})
	ev := a.waitAction(ActOrderMoved)
	p := ev["payload"].(map[string]any)

	o, ok := p["order"].(map[string]any)
	if !ok {
		t.Fatalf("改价广播里应当带改完的那张单：%v", p)
	}
	if o["id"] != orderID || o["price"].(float64) != 97.5 {
		t.Fatalf("改完的单应当是 %s @ 97.5，实际 %v @ %v", orderID, o["id"], o["price"])
	}
	if o["kind"] != KindLimit || o["quantity"].(float64) != 2 || o["status"] != OrderOpen {
		t.Fatalf("改价不该动单型/数量/状态：%v", o)
	}
	list := p["orders"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["price"].(float64) != 97.5 {
		t.Fatalf("全量列表里也应当是 97.5：%v", list)
	}
}

// 【新增实例（开盘）】走 create_instance 的 session 开关：锚点是**随机一天**的第一根，
// 而不是最新那一根 —— 所以它后面还有行情可以揭示，而且每次点都可能换一天。
func TestCreateSessionInstanceAnchorsAtDayOpen(t *testing.T) {
	hub, _ := newTestHub(t, 5) // 01-05 ~ 01-09，每天 09:00 ~ 11:00
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	anchors := map[string]bool{}
	for seed := 1; seed <= 5; seed++ {
		a.send(hub, map[string]any{
			"type": "create_instance", "symbol": testSymbol, "seed": seed,
			"session": true, "time": "2026-01-06 10:00", "bars": 40, "forwardDays": 1,
		})
		inst := a.waitFor(MsgInstanceState)["instance"].(map[string]any)
		anchor, _ := inst["anchor"].(string)
		// 首尾两天不参选（01-05 没历史、01-09 是尾日），且必须是当天的第 1 根
		if anchor != "2026-01-06 09:00" && anchor != "2026-01-07 09:00" && anchor != "2026-01-08 09:00" {
			t.Fatalf("开盘模式的锚点应当是中间某天的 09:00，实际 %q", anchor)
		}
		if inst["hiddenBars"].(float64) == 0 {
			t.Fatalf("开盘模式之后应当还有行情可揭示：%v", inst)
		}
		anchors[anchor] = true
	}
	if len(anchors) < 2 {
		t.Fatalf("5 个种子都落在同一天（%v），随机没有生效", anchors)
	}
}

// 【跳转到下一天开盘】走 next_day_open：一步到下一个交易日的第一根，
// 然后补一份完整状态下来（前端就是靠它把那一夜的 K 线补齐的）。
func TestNextDayOpenJumpsAndResendsState(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	id := createInstance(t, hub, a, "2026-01-06 10:00")
	a.drain()
	a.send(hub, map[string]any{"type": "next_day_open", "instanceId": id})

	ev := a.waitAction(ActBarAdvanced)
	p := ev["payload"].(map[string]any)
	if want := "2026-01-07 09:00"; p["bar"].(map[string]any)["time"] != want {
		t.Fatalf("应当跳到 %s，实际 %v", want, p["bar"].(map[string]any)["time"])
	}

	st := a.waitFor(MsgInstanceState)
	inst := st["instance"].(map[string]any)
	bars := inst["bars"].([]any)
	if want := int(p["cursor"].(float64)) + 1; len(bars) != want {
		t.Fatalf("补齐后的快照应当有 %d 根（游标 + 1），实际 %d", want, len(bars))
	}
	if last := bars[len(bars)-1].(map[string]any)["time"]; last != "2026-01-07 09:00" {
		t.Fatalf("快照最后一根应当是开盘那根，实际 %v", last)
	}
}
