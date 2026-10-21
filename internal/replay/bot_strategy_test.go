package replay

// 策略机器人的端到端测试：造几根能触发 Caveman 的 K 线，让机器人真的跑起来，
// 然后检查它有没有按规矩办事 —— 开仓、挂保护单、成交后撤另一张、日终平仓、不隔夜。
//
// 这里**不重新实现撮合**：所有断言看的都是 inst.Orders / inst.Trades / ClosedLots，
// 也就是复盘台那套撮合算出来的结果。如果哪天撮合改了，这些测试会跟着动。

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tradingview-j/internal/strategy"
)

// testBar 是一根手工指定的 K 线。high/low 不给就按 open/close 推。
type testBar struct {
	hm string // "09:00"
	o  float64
	h  float64
	l  float64
	c  float64
}

// testDay 是一天的 K 线。
type testDay struct {
	date string // "2026-01-05"
	bars []testBar
}

// writeCustomData 按真实落盘格式写一批指定内容的 CSV，返回数据目录。
// 目录结构、_symbols.json、时间格式都和 writeTestData 一致，只是内容由调用方定。
func writeCustomData(t *testing.T, days []testDay) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, testFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	idx := map[string]any{
		"version": 1,
		"symbols": map[string]any{testSymbol: map[string]any{
			"folder":    testFolder,
			"tick":      0.25,
			"tickValue": 0.5, // 一跳 0.25 点 = 0.5 美元，和 MNQ 一样
		}},
	}
	buf, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "_symbols.json"), buf, 0o644); err != nil {
		t.Fatalf("写索引失败：%v", err)
	}
	for _, day := range days {
		f, err := os.Create(filepath.Join(dir, day.date+".csv"))
		if err != nil {
			t.Fatalf("建 CSV 失败：%v", err)
		}
		fmt.Fprintln(f, "time,open,high,low,close,volume")
		for _, b := range day.bars {
			fmt.Fprintf(f, "%s %s,%s,%s,%s,%s,%d\n",
				day.date, b.hm, num(b.o), num(b.h), num(b.l), num(b.c), 100)
		}
		f.Close()
	}
	return root
}

// runStrategyBot 手动把实例挂上策略机器人，然后一拍一拍驱动到数据末尾。
//
// 不走协议、不起 goroutine：测试要的是确定性的时序，真实定时器只会让断言变成碰运气。
// 返回开了几单。
func runStrategyBot(t *testing.T, hub *Hub, inst *Instance, s strategy.Strategy, qty float64, maxTicks int) int {
	t.Helper()
	run := newStrategyRun(s, qty)
	inst.run = run
	inst.botStrategy = s.ID()
	inst.botQty = qty
	inst.botStop = make(chan struct{})
	inst.Operator = "bot"

	ticks := 0
	for ticks < maxTicks && inst.botStop != nil {
		hub.botStrategyTickLocked(inst, run)
		ticks++
	}
	if inst.botStop != nil {
		t.Fatalf("跑了 %d 拍还没到数据末尾", ticks)
	}
	return run.entries
}

func mountInstance(t *testing.T, hub *Hub, root, anchor string, history, forward int) *Instance {
	t.Helper()
	inst := openTestInstance(t, root, anchor, history, forward)
	hub.instances[inst.ID] = inst
	hub.order = append(hub.order, inst.ID)
	return inst
}

// 一天里最标准的做空剧本：强阳棒被阴棒吞回，之后一路跌到目标。
func bearishDay(date string) testDay {
	return testDay{date: date, bars: []testBar{
		{hm: "09:00", o: 100, h: 100.5, l: 99.5, c: 100},    // 基数棒
		{hm: "09:05", o: 100, h: 102, l: 99.8, c: 101.8},    // 强阳：实体 82%、收在 91%
		{hm: "09:10", o: 101.8, h: 101.9, l: 99.0, c: 99.2}, // 阴棒收在 99.2 < 100：吞回起点 → 做空
		{hm: "09:15", o: 99.2, h: 99.5, l: 96.0, c: 96.5},   // 跌到 96.0，打中目标 96.25
		{hm: "09:20", o: 96.5, h: 96.7, l: 96.3, c: 96.5},
		{hm: "09:25", o: 96.5, h: 96.7, l: 96.3, c: 96.5},
		{hm: "09:30", o: 96.5, h: 96.7, l: 96.3, c: 96.5},
		{hm: "09:35", o: 96.5, h: 96.7, l: 96.3, c: 96.5},
	}}
}

// 开仓之后立刻反着走：止损被打掉，仓位归零，止盈单必须被撤掉。
func stoppedDay(date string) testDay {
	return testDay{date: date, bars: []testBar{
		{hm: "09:00", o: 100, h: 100.5, l: 99.5, c: 100},
		{hm: "09:05", o: 100, h: 102, l: 99.8, c: 101.8},
		{hm: "09:10", o: 101.8, h: 101.9, l: 99.0, c: 99.2}, // 做空 99.2，止损 102.25，目标 96.25
		{hm: "09:15", o: 99.2, h: 103.0, l: 99.0, c: 102.8}, // 冲过 102.25：止损成交
		{hm: "09:20", o: 102.8, h: 103.0, l: 102.6, c: 102.8},
		{hm: "09:25", o: 102.8, h: 103.0, l: 102.6, c: 102.8},
	}}
}

// 完整跑一天：应当开出一单空单、挂上止损和止盈、最后被止盈平掉并撤掉止损。
func TestStrategyBotTradesWithProtection(t *testing.T) {
	root := writeCustomData(t, []testDay{bearishDay("2026-01-05")})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 09:00", 1, 1)

	s, err := strategy.New("caveman", nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := runStrategyBot(t, hub, inst, s, 1, 40)
	if entries != 1 {
		t.Fatalf("这一天应当只开 1 单，实际 %d 单", entries)
	}

	// 开仓是一张市价卖单；保护单应当是一张 stop + 一张 limit。
	var markets, stops, limits []*Order
	for i := range inst.Orders {
		o := &inst.Orders[i]
		switch o.Kind {
		case KindMarket:
			markets = append(markets, o)
		case KindStop:
			stops = append(stops, o)
		case KindLimit:
			limits = append(limits, o)
		}
	}
	if len(markets) != 1 || markets[0].Side != "sell" {
		t.Fatalf("应当只有一张市价卖单开仓，实际 %+v", inst.Orders)
	}
	if len(stops) != 1 || stops[0].Side != "buy" {
		t.Fatalf("应当挂一张 buy stop 当止损，实际 %+v", stops)
	}
	if len(limits) != 1 || limits[0].Side != "buy" {
		t.Fatalf("应当挂一张 buy limit 当止盈，实际 %+v", limits)
	}
	// 价格跌到 96.0 打中止盈，止损随后被撤 —— 这一撤就是 OCO，缺了它
	// 剩下那张单会在平仓之后又把仓位开回来。
	if limits[0].Status != OrderFilled {
		t.Fatalf("止盈单应当成交，实际 %s", limits[0].Status)
	}
	if stops[0].Status != OrderCancelled || stops[0].FillPrice != 0 {
		t.Fatalf("止盈成交后止损应当被撤（不成交），实际 %+v", stops[0])
	}
	if inst.Position.Quantity != 0 {
		t.Fatalf("收盘后应当空仓，实际持仓 %v", inst.Position.Quantity)
	}
	// 空头 99.2 进、96.0 出：R=3.05，目标 96.15 向下对齐一格 = 96.0，2 美元/点
	if math.Abs(inst.RealizedPnl-6.4) > 0.01 {
		t.Fatalf("已实现盈亏应当是 6.4 美元（(99.2-96.0)×2），实际 %v", inst.RealizedPnl)
	}
}

// 止损路径：价格反向冲过去，止损成交，剩下那张止盈单必须被撤掉。
// 这条不测的话，「平完仓之后另一张单又把仓位开回来」这种 bug 会一直藏着。
func TestStrategyBotUsesStopLoss(t *testing.T) {
	root := writeCustomData(t, []testDay{stoppedDay("2026-01-05")})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 09:00", 1, 1)

	s, _ := strategy.New("caveman", nil)
	runStrategyBot(t, hub, inst, s, 1, 40)

	// 止损平掉之后同一根 K 线上策略可能又看到反手形态（这里就开了一张多单），
	// 所以只认**第一张**保护单 —— 后面那套属于第二次交易，不在这个测试的范围内。
	var stop, target *Order
	for i := range inst.Orders {
		switch {
		case inst.Orders[i].Kind == KindStop && stop == nil:
			stop = &inst.Orders[i]
		case inst.Orders[i].Kind == KindLimit && target == nil:
			target = &inst.Orders[i]
		}
	}
	if stop == nil || stop.Status != OrderFilled {
		t.Fatalf("止损单应当成交，实际 %+v", stop)
	}
	// 止损价 102.25，跳空高开在 103 以内，成交价就是挂单价
	if math.Abs(stop.FillPrice-102.25) > 1e-9 {
		t.Fatalf("止损成交价应当是 102.25，实际 %v", stop.FillPrice)
	}
	if target == nil || target.Status != OrderCancelled {
		t.Fatalf("止损成交后止盈单应当被撤掉，实际 %+v", target)
	}
	if inst.Position.Quantity != 0 {
		t.Fatalf("止损之后应当空仓，实际 %v", inst.Position.Quantity)
	}
	if inst.RealizedPnl >= 0 {
		t.Fatalf("止损离场应当是亏的，实际 %v", inst.RealizedPnl)
	}
}

// 不隔夜：仓位一直没被打掉，走到交易日收盘那根必须被强平。
//
// 数据造的是「开仓之后再没动过」—— 平台横盘，止损和止盈都不会碰到。
func TestStrategyBotFlattensBeforeSessionEnd(t *testing.T) {
	// 04:40 开出空单，之后平台横盘碰不到止损止盈；04:50 进收尾时段必须强平。
	// 06:00 那几根是新交易日，用来验证「平掉之后就真的不剩仓位」。
	day := testDay{date: "2026-01-05", bars: []testBar{
		{hm: "04:30", o: 100, h: 100.5, l: 99.5, c: 100},
		{hm: "04:35", o: 100, h: 102, l: 99.8, c: 101.8},
		{hm: "04:40", o: 101.8, h: 101.9, l: 99.0, c: 99.2}, // 做空 99.2
		{hm: "04:45", o: 99.2, h: 99.4, l: 99.0, c: 99.2},
		{hm: "04:50", o: 99.2, h: 99.4, l: 99.0, c: 99.2}, // 收尾时段：强平
		{hm: "04:55", o: 99.2, h: 99.4, l: 99.0, c: 99.2},
		{hm: "06:00", o: 99.2, h: 99.4, l: 99.0, c: 99.2}, // 新交易日
		{hm: "06:05", o: 99.2, h: 99.4, l: 99.0, c: 99.2},
	}}
	root := writeCustomData(t, []testDay{day})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 04:30", 1, 1)

	s, _ := strategy.New("caveman", nil)
	runStrategyBot(t, hub, inst, s, 1, 40)

	if inst.Position.Quantity != 0 {
		t.Fatalf("收尾时段应当已经平仓，实际持仓 %v", inst.Position.Quantity)
	}
	var markets []Trade
	for _, tr := range inst.Trades {
		if tr.Kind == KindMarket {
			markets = append(markets, tr)
		}
	}
	if len(markets) != 2 {
		t.Fatalf("应当有开仓 + 强平两笔市价成交，实际 %d 笔：%+v", len(markets), markets)
	}
	if markets[1].Time != "2026-01-05 04:50" {
		t.Fatalf("强平应当发生在 04:50 那根，实际 %s", markets[1].Time)
	}
	// 99.2 进、99.2 出，这单是平的
	if math.Abs(markets[1].RealizedPnl) > 1e-9 {
		t.Fatalf("同价进出不该有盈亏，实际 %v", markets[1].RealizedPnl)
	}
}

// 机器人跑到数据末尾要自己停，不能留在「操作中」把界面卡住。
func TestStrategyBotStopsAtDataEnd(t *testing.T) {
	root := writeCustomData(t, []testDay{bearishDay("2026-01-05")})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 09:00", 1, 1)

	s, _ := strategy.New("caveman", nil)
	// runStrategyBot 内部就断言了「跑到 botStop 被关掉」
	runStrategyBot(t, hub, inst, s, 1, 40)

	if inst.run != nil {
		t.Fatal("数据到末尾后策略状态应当被清掉")
	}
	if inst.botStrategy != "" {
		t.Fatalf("停止后不该还记着策略：%q", inst.botStrategy)
	}
	if inst.Operator != "" {
		t.Fatalf("自动停止后操作权应当还给「无人」，实际 %q", inst.Operator)
	}
}

// 策略清单走协议：前端启动面板就是靠这条消息把策略列出来的。
func TestListStrategiesOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	a.send(hub, map[string]any{"type": "list_strategies"})
	msg := a.waitFor(MsgStrategies)
	raw, ok := msg["strategies"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("策略清单不该为空：%v", msg["strategies"])
	}
	var found bool
	for _, item := range raw {
		m := item.(map[string]any)
		if m["id"] != "caveman" {
			continue
		}
		found = true
		if m["name"] == "" || m["summary"] == "" {
			t.Fatalf("策略描述不完整：%v", m)
		}
		params, _ := m["params"].([]any)
		if len(params) == 0 {
			t.Fatalf("caveman 应当带参数描述：%v", m)
		}
	}
	if !found {
		t.Fatalf("清单里应当有 caveman：%v", raw)
	}
}

// 启动/停止的完整协议流程：策略 id 要出现在广播和实例快照里，停止后清空。
func TestStrategyBotStartStopOverProtocol(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-05 10:00")

	a.send(hub, map[string]any{
		"type":       "bot_start",
		"instanceId": id,
		"strategy":   "caveman",
		"quantity":   2,
		"intervalMs": 80,
	})
	started := a.waitAction(ActBotStarted)
	payload, _ := started["payload"].(map[string]any)
	if payload["strategy"] != "caveman" {
		t.Fatalf("启动广播里应当带策略 id，实际 %v", payload)
	}
	if payload["quantity"] != float64(2) {
		t.Fatalf("启动广播里应当带手数 2，实际 %v", payload["quantity"])
	}

	a.send(hub, map[string]any{"type": "list_instances"})
	msg := a.waitFor(MsgInstances)
	list := msg["instances"].([]any)
	row := list[0].(map[string]any)
	if row["botStrategy"] != "caveman" {
		t.Fatalf("实例列表里应当显示正在跑的策略，实际 %v", row)
	}
	if row["bot"] != true {
		t.Fatal("实例列表里应当显示机器人在跑")
	}

	a.send(hub, map[string]any{"type": "bot_stop", "instanceId": id})
	a.waitAction(ActBotStopped)
	if inst := hub.instances[id]; inst == nil || inst.botStrategy != "" || inst.run != nil {
		t.Fatalf("停止后策略状态应当清空：%+v", hub.instances[id])
	}
}

// 策略 id 打错时不能接管操作权：否则界面显示「机器人操作中」，其实什么都没跑。
func TestBotStartRejectsUnknownStrategy(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-05 10:00")

	a.send(hub, map[string]any{"type": "bot_start", "instanceId": id, "strategy": "没有这个策略"})
	e := a.waitFor(MsgError)
	if e["message"] == "" {
		t.Fatal("未知策略应当返回错误")
	}
	inst := hub.instances[id]
	if inst.botStop != nil || inst.Operator == "bot" {
		t.Fatalf("策略创建失败时不该接管操作权：%+v", inst)
	}
}

// 手数不合法（小数）同样在接管之前就被拒。
func TestBotStartRejectsBadQuantity(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	id := createInstance(t, hub, a, "2026-01-05 10:00")

	a.send(hub, map[string]any{
		"type": "bot_start", "instanceId": id, "strategy": "caveman", "quantity": 1.5,
	})
	if e := a.waitFor(MsgError); e["message"] == "" {
		t.Fatal("小数手数应当被拒")
	}
	if inst := hub.instances[id]; inst.botStop != nil {
		t.Fatal("手数不合法时不该启动机器人")
	}
}

// 一根长影线同时扎穿止损和止盈时，机器人的两张保护单**只能成交一张**。
//
// 不测这条的后果很隐蔽：仓位会被平过头变成反向仓，而且成交流水里看起来
// 只是「多了一笔」，很容易被当成策略的正常反手。真实数据上一天能出现好几次。
func TestStrategyBotProtectionIsOCO(t *testing.T) {
	day := testDay{date: "2026-01-05", bars: []testBar{
		{hm: "09:00", o: 100, h: 100.5, l: 99.5, c: 100},
		{hm: "09:05", o: 100, h: 102, l: 99.8, c: 101.8},
		{hm: "09:10", o: 101.8, h: 101.9, l: 99.0, c: 99.2}, // 做空 99.2，止损 102.25，目标 96.0
		// 这一根最高 103（打穿止损）、最低 95.5（也打穿止盈）：5 分钟数据判不出先后
		{hm: "09:15", o: 99.2, h: 103.0, l: 95.5, c: 101.0},
		{hm: "09:20", o: 101.0, h: 101.2, l: 100.8, c: 101.0},
	}}
	root := writeCustomData(t, []testDay{day})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 09:00", 1, 1)

	s, _ := strategy.New("caveman", nil)
	runStrategyBot(t, hub, inst, s, 1, 40)

	var stop, limit *Order
	for i := range inst.Orders {
		switch inst.Orders[i].Kind {
		case KindStop:
			stop = &inst.Orders[i]
		case KindLimit:
			limit = &inst.Orders[i]
		}
	}
	if stop == nil || limit == nil {
		t.Fatalf("两张保护单都该挂上：%+v", inst.Orders)
	}
	if stop.Status != OrderFilled {
		t.Fatalf("先挂的止损单应当成交，实际 %s", stop.Status)
	}
	if limit.Status != OrderCancelled {
		t.Fatalf("止盈单应当被 OCO 自动撤销，实际 %s", limit.Status)
	}
	if inst.Position.Quantity != 0 {
		t.Fatalf("平掉之后不该有反向仓，实际持仓 %v", inst.Position.Quantity)
	}
	// 空头 99.2 进、102.25 止损出：3.05 点 × 2 美元/点
	if math.Abs(inst.RealizedPnl+6.1) > 0.01 {
		t.Fatalf("这一单应当亏 6.1 美元，实际 %v", inst.RealizedPnl)
	}
}
