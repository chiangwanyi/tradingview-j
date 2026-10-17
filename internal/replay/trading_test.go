package replay

import (
	"math"
	"testing"
	"time"
)

// newTradingInstance 造一条可控的行情：三根 5 分钟 K 线，价格都写得明明白白。
func newTradingInstance() *Instance {
	bars := []Bar{
		{Time: "2026-01-05 09:00", Open: 100, High: 101, Low: 99, Close: 100},
		{Time: "2026-01-05 09:05", Open: 100, High: 102, Low: 98, Close: 101},
		{Time: "2026-01-05 09:10", Open: 101, High: 103, Low: 97, Close: 102},
	}
	return &Instance{ID: "ins-t", Symbol: testSymbol, Folder: testFolder, Bars: bars, Cursor: 0, Operator: "human"}
}

func emptyDS() *DataSource { return NewDataSource("", time.Local) }

func TestLimitBuyFillsAtOrderPrice(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	if _, err := inst.placeOrder("human", "buy", KindLimit, 97.5, 2); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	// 下一根（09:05）最低 98 > 97.5，不该成交
	if _, fills, err := inst.advance(ds); err != nil || len(fills) != 0 {
		t.Fatalf("09:05 不该成交：fills=%v err=%v", fills, err)
	}
	// 再下一根（09:10）最低 97 <= 97.5，成交
	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("应当成交 1 笔，实际 %d", len(fills))
	}
	if fills[0].Price != 97.5 {
		t.Fatalf("成交价应当等于挂单价 97.5，实际 %v", fills[0].Price)
	}
	if inst.Position.Quantity != 2 || inst.Position.Side != "long" || inst.Position.AveragePrice != 97.5 {
		t.Fatalf("持仓不对：%+v", inst.Position)
	}
	if inst.RealizedPnl != 0 {
		t.Fatalf("开仓不该产生已实现盈亏，实际 %v", inst.RealizedPnl)
	}
	if inst.Orders[0].Status != OrderFilled || inst.Orders[0].FilledAt != "2026-01-05 09:10" {
		t.Fatalf("订单状态不对：%+v", inst.Orders[0])
	}
}

func TestLimitSellClosesAndRealizesPnl(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	inst.applyFill(Order{ID: "o0", Side: "buy", Quantity: 1, Price: 100}, inst.Bars[0].Time)
	if inst.Position.Quantity != 1 || inst.Position.AveragePrice != 100 {
		t.Fatalf("准备持仓失败：%+v", inst.Position)
	}
	if _, err := inst.placeOrder("human", "sell", KindLimit, 102.5, 1); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	if _, _, err := inst.advance(ds); err != nil { // 09:05 最高 102，不成交
		t.Fatalf("步进失败：%v", err)
	}
	if inst.Position.Quantity != 1 {
		t.Fatalf("不该提前平仓：%+v", inst.Position)
	}
	_, fills, err := inst.advance(ds) // 09:10 最高 103 >= 102.5
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 || fills[0].RealizedPnl != 2.5 {
		t.Fatalf("平仓盈亏应当是 2.5，实际 %+v", fills)
	}
	if inst.Position.Quantity != 0 || inst.Position.Side != "flat" {
		t.Fatalf("应当回到空仓：%+v", inst.Position)
	}
	if math.Abs(inst.RealizedPnl-2.5) > 1e-9 {
		t.Fatalf("累计已实现盈亏应当是 2.5，实际 %v", inst.RealizedPnl)
	}
}

func TestStopOrdersTriggerOnBreakout(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	// 止损买：最高价突破 102.5 才成交
	if _, err := inst.placeOrder("human", "buy", KindStop, 102.5, 1); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	if _, fills, _ := inst.advance(ds); len(fills) != 0 { // 09:05 最高 102
		t.Fatalf("09:05 最高 102 不该触发 102.5 的止损买：%v", fills)
	}
	_, fills, err := inst.advance(ds) // 09:10 最高 103
	if err != nil || len(fills) != 1 {
		t.Fatalf("09:10 应当触发止损买：fills=%v err=%v", fills, err)
	}
	if inst.Position.Quantity != 1 || inst.Position.AveragePrice != 102.5 {
		t.Fatalf("持仓不对：%+v", inst.Position)
	}

	// 止损卖：最低价跌破 99.5 才成交（09:05 最低 98 就跌破）
	inst2 := newTradingInstance()
	if _, err := inst2.placeOrder("human", "sell", KindStop, 99.5, 1); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	_, fills2, err := inst2.advance(emptyDS())
	if err != nil || len(fills2) != 1 {
		t.Fatalf("09:05 最低 98 应当触发 99.5 的止损卖：fills=%v err=%v", fills2, err)
	}
	if inst2.Position.Quantity != -1 || inst2.Position.AveragePrice != 99.5 {
		t.Fatalf("空头持仓不对：%+v", inst2.Position)
	}
}

func TestPositionFlipRealizesAndReopens(t *testing.T) {
	inst := newTradingInstance()
	inst.applyFill(Order{ID: "o0", Side: "buy", Quantity: 1, Price: 100}, inst.Bars[0].Time)
	tr := inst.applyFill(Order{ID: "o1", Side: "sell", Quantity: 3, Price: 110}, inst.Bars[0].Time)
	if math.Abs(tr.RealizedPnl-10) > 1e-9 {
		t.Fatalf("平掉 1 手应当实现 10，实际 %v", tr.RealizedPnl)
	}
	if inst.Position.Quantity != -2 {
		t.Fatalf("反手后应当是 -2，实际 %v", inst.Position.Quantity)
	}
	if inst.Position.AveragePrice != 110 {
		t.Fatalf("反手后均价应当是 110，实际 %v", inst.Position.AveragePrice)
	}
	// 空头浮动盈亏：(close - 110) * -2
	inst.Cursor = 2 // close 102
	if got := inst.unrealizedPnl(); math.Abs(got-16) > 1e-9 {
		t.Fatalf("空头浮盈应当是 16，实际 %v", got)
	}
}

func TestOrderValidation(t *testing.T) {
	inst := newTradingInstance()
	cases := []struct {
		side, kind string
		price, qty float64
	}{
		{"hold", KindLimit, 100, 1},
		{"buy", "market", 100, 1},
		{"buy", KindLimit, 0, 1},
		{"buy", KindLimit, 100, 0},
		{"buy", KindLimit, -5, 1},
	}
	for _, c := range cases {
		if _, err := inst.placeOrder("human", c.side, c.kind, c.price, c.qty); err == nil {
			t.Fatalf("这些参数本该报错：%+v", c)
		}
	}
}

func TestCancelOrder(t *testing.T) {
	inst := newTradingInstance()
	o, _ := inst.placeOrder("human", "buy", KindLimit, 99, 1)
	got, err := inst.cancelOrder(o.ID)
	if err != nil || got.Status != OrderCancelled {
		t.Fatalf("撤单失败：%+v %v", got, err)
	}
	if _, err := inst.cancelOrder(o.ID); err == nil {
		t.Fatal("重复撤单应当报错")
	}
	// 撤销后不再参与撮合
	if _, fills, _ := inst.advance(emptyDS()); len(fills) != 0 {
		t.Fatalf("撤掉的单不该成交：%v", fills)
	}
}

func TestOrderPlacedThisBarWaitsNextBar(t *testing.T) {
	inst := newTradingInstance()
	// 游标停在 09:05（最低 98）：此刻挂一张 99 的限价买，本根已经收完，不该成交
	inst.Cursor = 1
	if _, err := inst.placeOrder("human", "buy", KindLimit, 99, 1); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	if len(inst.Trades) != 0 {
		t.Fatal("挂单的那一刻不该成交")
	}
	_, fills, err := inst.advance(emptyDS()) // 下一根 09:10 最低 97 -> 成交
	if err != nil || len(fills) != 1 {
		t.Fatalf("下一根应当成交：fills=%v err=%v", fills, err)
	}
}
