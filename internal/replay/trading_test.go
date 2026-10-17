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
	inst.applyFill(Order{ID: "o0", Side: "buy", Quantity: 1, Price: 100}, 100, inst.Bars[0].Time)
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
	inst.applyFill(Order{ID: "o0", Side: "buy", Quantity: 1, Price: 100}, 100, inst.Bars[0].Time)
	tr := inst.applyFill(Order{ID: "o1", Side: "sell", Quantity: 3, Price: 110}, 110, inst.Bars[0].Time)
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

// TestFillPriceUsesOpenOnGap 钉住「成交价怎么来」。
//
// 触发看的是整根 K 线的高低点，但成交价必须区分「有没有跳空」：
// 开盘就已经越过挂单价时，真实成交发生在开盘那一刻，按挂单价成交等于后视镜里占便宜。
func TestFillPriceUsesOpenOnGap(t *testing.T) {
	cases := []struct {
		name       string
		side, kind string
		order      float64
		bar        Bar
		want       float64
	}{
		// 没跳空：开盘还在挂单价的「来路」上 —— 成交价就是挂单价
		{"限价买·无跳空", "buy", KindLimit, 97.5, Bar{Open: 100, High: 101, Low: 97, Close: 98}, 97.5},
		{"限价卖·无跳空", "sell", KindLimit, 102.5, Bar{Open: 100, High: 103, Low: 99, Close: 102}, 102.5},
		{"止损买·无跳空", "buy", KindStop, 102.5, Bar{Open: 100, High: 103, Low: 99, Close: 102}, 102.5},
		{"止损卖·无跳空", "sell", KindStop, 99.5, Bar{Open: 100, High: 101, Low: 98, Close: 98}, 99.5},
		// 跳空：开盘已经在挂单价之外，第一笔成交就在开盘价
		{"限价买·跳空低开", "buy", KindLimit, 97.5, Bar{Open: 96, High: 98, Low: 95, Close: 97}, 96},
		{"限价卖·跳空高开", "sell", KindLimit, 102.5, Bar{Open: 104, High: 105, Low: 103, Close: 104}, 104},
		{"止损买·跳空高开", "buy", KindStop, 102.5, Bar{Open: 105, High: 106, Low: 104, Close: 105}, 105},
		{"止损卖·跳空低开", "sell", KindStop, 97.5, Bar{Open: 96, High: 97, Low: 95, Close: 96}, 96},
	}
	for _, c := range cases {
		got := fillPriceFor(Order{Side: c.side, Kind: c.kind, Price: c.order}, c.bar)
		if math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%s：成交价应当是 %v，实际 %v", c.name, c.want, got)
		}
	}
}

// TestGapFillUpdatesOrderTradeAndPosition 确认跳空成交价一路传到订单、成交流水和持仓成本，
// 而不只是改一个显示字段 —— 盈亏是按持仓均价算的，漏传就会算出假盈利。
func TestGapFillUpdatesOrderTradeAndPosition(t *testing.T) {
	inst := &Instance{
		ID: "ins-gap", Symbol: testSymbol, Folder: testFolder, Operator: "human",
		Bars: []Bar{
			{Time: "2026-01-05 09:00", Open: 100, High: 101, Low: 99, Close: 100},
			{Time: "2026-01-05 09:05", Open: 96, High: 98, Low: 95, Close: 97}, // 直接低开到 97.5 之下
		},
	}
	if _, err := inst.placeOrder("human", "buy", KindLimit, 97.5, 2); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	_, fills, err := inst.advance(emptyDS())
	if err != nil || len(fills) != 1 {
		t.Fatalf("应当成交 1 笔：fills=%v err=%v", fills, err)
	}
	if fills[0].Price != 96 {
		t.Fatalf("跳空低开时成交价应当是开盘价 96，实际 %v", fills[0].Price)
	}
	if o := inst.Orders[0]; o.FillPrice != 96 || o.Price != 97.5 {
		t.Fatalf("订单上的成交价应当是 96、挂单价仍是 97.5：%+v", o)
	}
	if inst.Position.AveragePrice != 96 || inst.Position.Quantity != 2 {
		t.Fatalf("持仓成本应当按成交价 96 算：%+v", inst.Position)
	}
}

// TestStopBuyBelowMarketFillsAtOpen 钉住「挂反方向不许白捡」。
//
// 现价 100 挂一张 95 的止损买：真实市场里这张单在挂上的那一刻就已经触发（是市价买单），
// 成交价只能是当时的市价，不能是更便宜的 95。这里用下一根的开盘价当市价。
func TestStopBuyBelowMarketFillsAtOpen(t *testing.T) {
	inst := newTradingInstance() // 游标 0：现价 100
	if _, err := inst.placeOrder("human", "buy", KindStop, 95, 1); err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	_, fills, err := inst.advance(emptyDS()) // 09:05 开 100 / 高 102 / 低 98
	if err != nil || len(fills) != 1 {
		t.Fatalf("09:05 应当立即成交：fills=%v err=%v", fills, err)
	}
	if fills[0].Price != 100 {
		t.Fatalf("止损买挂在现价下方应当按开盘价 100 成交（等于市价买），实际 %v", fills[0].Price)
	}
}
