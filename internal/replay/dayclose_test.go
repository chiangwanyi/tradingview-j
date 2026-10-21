package replay

// 日终硬约束：**每天 23:55（日历日最后一根 K 线）无条件平仓，任何仓位都不留过夜。**
//
// 这一组测试不依赖 Caveman：用一个「每根都出信号」的桩策略，把注意力放在执行器的
// 时序上 —— 什么时候平、平在哪一格的价、平完还撤不撤单、下一根会不会又开仓。
//
// 时间全部取自 CSV 里的本地明文（北京时间），断言也直接比 "2026-01-05 23:55" 这种字符串，
// 中间没有任何时区换算，所以「以数据库时间为准」是结构性成立的，不是靠约定。

import (
	"math"
	"testing"
	"time"

	"tradingview-j/internal/strategy"
)

// stubStrategy 是一个只为了驱动执行器的假策略：空仓时每根都要买。
// stop/target 放得很远，保证不会在测试里意外成交，从而只留下「日终平仓」这一条路径。
type stubStrategy struct{}

func (stubStrategy) ID() string                      { return "stub" }
func (stubStrategy) Name() string                    { return "桩策略" }
func (stubStrategy) Summary() string                 { return "每根都出信号，只为测执行器时序" }
func (stubStrategy) Params() []strategy.Param        { return nil }
func (stubStrategy) Configure(strategy.Values) error { return nil }
func (stubStrategy) Reset()                          {}
func (stubStrategy) OnBar(*strategy.Context) []strategy.Signal {
	return []strategy.Signal{{Side: "buy", Stop: 90, Target: 110, Reason: "桩"}}
}

// startStrategyBot 把策略机器人挂到实例上，但不驱动 —— 测试要自己控制每一拍。
func startStrategyBot(inst *Instance, s strategy.Strategy, qty float64) *strategyRun {
	run := newStrategyRun(s, qty)
	inst.run = run
	inst.botStrategy = s.ID()
	inst.botQty = qty
	inst.botStop = make(chan struct{})
	inst.Operator = "bot"
	return run
}

// tickUntil 一拍一拍推进，直到游标停在 barTime 那根上。到不了就报错。
func tickUntil(t *testing.T, hub *Hub, inst *Instance, run *strategyRun, barTime string, max int) {
	t.Helper()
	for i := 0; i < max; i++ {
		hub.botStrategyTickLocked(inst, run)
		if inst.botStop == nil {
			t.Fatalf("机器人提前停了（第 %d 拍），没走到 %s", i+1, barTime)
		}
		if inst.currentBar().Time == barTime {
			return
		}
	}
	t.Fatalf("跑了 %d 拍还是没走到 %s，当前 %s", max, barTime, inst.currentBar().Time)
}

// 一个日历日的最后几根 + 次日开头。23:55 那根把价格拉到 102.5，
// 用来验证「平仓价就是这一根的收盘价」。
func dayCloseDays() []testDay {
	return []testDay{
		{date: "2026-01-05", bars: []testBar{
			{hm: "23:40", o: 100, h: 100.5, l: 99.5, c: 100},
			{hm: "23:45", o: 100, h: 100.6, l: 99.8, c: 100}, // 开仓在这根
			{hm: "23:50", o: 100, h: 100.5, l: 99.9, c: 100.2},
			{hm: "23:55", o: 100.2, h: 102.6, l: 100.1, c: 102.5}, // 日终：平在这
		}},
		{date: "2026-01-06", bars: []testBar{
			{hm: "00:00", o: 102.5, h: 102.8, l: 102.2, c: 102.6}, // 新的一天，可以再开
			{hm: "00:05", o: 102.6, h: 102.9, l: 102.3, c: 102.7},
		}},
	}
}

// 23:55 那根必须把仓位平掉，而且**平完不能在同一根再开一张**。
func TestBotFlattensAtDayClose(t *testing.T) {
	root := writeCustomData(t, dayCloseDays())
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 23:40", 1, 1)
	run := startStrategyBot(inst, stubStrategy{}, 1)

	// 走到 23:50：应该已经有一张多单了。
	tickUntil(t, hub, inst, run, "2026-01-05 23:50", 10)
	if inst.Position.Quantity != 1 {
		t.Fatalf("23:50 该有多单 1 手，实际 %v", inst.Position.Quantity)
	}

	// 23:55：日终强平。
	tickUntil(t, hub, inst, run, "2026-01-05 23:55", 10)
	if inst.Position.Quantity != 0 {
		t.Fatalf("23:55 必须平光，实际持仓 %v", inst.Position.Quantity)
	}
	if run.entries != 1 {
		t.Fatalf("23:55 不该再开新仓，实际开仓 %d 次", run.entries)
	}
	// 100 进、102.5 出，2.5 点 × 2 美元/点
	if math.Abs(inst.RealizedPnl-5) > 0.001 {
		t.Fatalf("23:55 收盘价 102.5 平仓应当赚 5.00，实际 %v", inst.RealizedPnl)
	}
	// 平完盘面上不能留任何挂单：留下来的那张会在 00:00 之后被触发，凭空开出一个过夜仓。
	for i := range inst.Orders {
		if inst.Orders[i].Status == OrderOpen {
			t.Fatalf("日终平仓后不该有挂着的单：%+v", inst.Orders[i])
		}
	}

	// 下一根是次日 00:00 —— 新的一天，允许重新开仓（这不算过夜）。
	tickUntil(t, hub, inst, run, "2026-01-06 00:00", 10)
	if inst.Position.Quantity != 1 || run.entries != 2 {
		t.Fatalf("次日 00:00 应当重新开仓：持仓 %v，开仓 %d 次", inst.Position.Quantity, run.entries)
	}
}

// 日终平仓要连带撤掉**盘面上所有**挂单，不只是机器人自己那两张保护单。
func TestDayCloseCancelsLeftoverOrders(t *testing.T) {
	root := writeCustomData(t, dayCloseDays())
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 23:40", 1, 1)

	// 模拟「机器人启动之前用户自己挂的一张远单」，价格远到永远不会成交。
	leftover, err := inst.placeOrder("human", "buy", KindLimit, 50, 1)
	if err != nil {
		t.Fatalf("挂单失败：%v", err)
	}

	run := startStrategyBot(inst, stubStrategy{}, 1)
	tickUntil(t, hub, inst, run, "2026-01-05 23:55", 10)

	var got *Order
	for i := range inst.Orders {
		if inst.Orders[i].ID == leftover.ID {
			got = &inst.Orders[i]
		}
	}
	if got == nil {
		t.Fatalf("找不到那张遗留挂单")
	}
	if got.Status != OrderCancelled {
		t.Fatalf("日终该把遗留挂单一起撤掉，实际 %s", got.Status)
	}
}

// 上一日缺了 23:55 那根（半天市/数据缺口）：跨日历日时兜底平仓，
// 成交价是次日前第一根的收盘价 —— 会吃一次跳空，但绝不带着仓位继续跑。
func TestDayCloseFallbackWhenLastBarMissing(t *testing.T) {
	root := writeCustomData(t, []testDay{
		{date: "2026-01-05", bars: []testBar{
			{hm: "11:50", o: 100, h: 100.4, l: 99.8, c: 100},   // 锚点
			{hm: "11:55", o: 100, h: 100.5, l: 99.5, c: 100},   // 开仓
			{hm: "12:00", o: 100, h: 100.4, l: 99.8, c: 100.1}, // 当天最后一根，没有 23:55
		}},
		{date: "2026-01-06", bars: []testBar{
			{hm: "00:00", o: 97, h: 97.2, l: 96.8, c: 97}, // 隔夜跳空低开
			{hm: "00:05", o: 97, h: 97.2, l: 96.8, c: 97},
		}},
	})
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	inst := mountInstance(t, hub, root, "2026-01-05 11:50", 1, 1)
	run := startStrategyBot(inst, stubStrategy{}, 1)

	tickUntil(t, hub, inst, run, "2026-01-05 12:00", 10)
	if inst.Position.Quantity != 1 {
		t.Fatalf("12:00 该还持着仓（还没到日终），实际 %v", inst.Position.Quantity)
	}

	// 跨到次日：兜底强平在 00:00 这根上，按 97 成交 —— 亏 (97-100)×2 = -6.00
	tickUntil(t, hub, inst, run, "2026-01-06 00:00", 10)
	if inst.Position.Quantity != 0 {
		t.Fatalf("跨日兜底必须平光，实际持仓 %v", inst.Position.Quantity)
	}
	if math.Abs(inst.RealizedPnl+6) > 0.001 {
		t.Fatalf("按 00:00 收盘价 97 平仓应当亏 6.00，实际 %v", inst.RealizedPnl)
	}
}
