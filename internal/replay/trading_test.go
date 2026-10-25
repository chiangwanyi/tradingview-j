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

// TestMarketOrderFillsImmediately 钉住市价单的语义：**下单那一刻就成交**，
// 成交价是当前（游标那根）K 线的收盘价，不等下一根，也不会被 advance 再撮合一次。
func TestMarketOrderFillsImmediately(t *testing.T) {
	inst := newTradingInstance() // 游标 09:00，收盘 100

	o, tr, err := inst.marketOrder("human", "buy", 2)
	if err != nil {
		t.Fatalf("市价买失败：%v", err)
	}
	if o.Kind != KindMarket || o.Status != OrderFilled || o.FillPrice != 100 || o.Price != 100 {
		t.Fatalf("市价单应当立刻按 100 成交：%+v", o)
	}
	if o.FilledIndex != inst.Cursor || o.PlacedIndex != inst.Cursor {
		t.Fatalf("成交下标应当就是当前游标：%+v", o)
	}
	if tr.Price != 100 || tr.Quantity != 2 || tr.Kind != KindMarket {
		t.Fatalf("成交流水不对：%+v", tr)
	}
	if inst.Position.Quantity != 2 || inst.Position.Side != "long" || inst.Position.AveragePrice != 100 {
		t.Fatalf("持仓不对：%+v", inst.Position)
	}
	if inst.RealizedPnl != 0 {
		t.Fatalf("开仓不该产生已实现盈亏，实际 %v", inst.RealizedPnl)
	}

	// 连着步进两根：已经 filled 的单不该再成交第二次
	if _, _, err := inst.advance(emptyDS()); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if _, _, err := inst.advance(emptyDS()); err != nil { // 09:10 收盘 102
		t.Fatalf("步进失败：%v", err)
	}
	if len(inst.Trades) != 1 {
		t.Fatalf("市价单只该成交一次，实际 %d 笔", len(inst.Trades))
	}

	// 现价 102 市价平掉 1 手：实现 (102-100)×1×1 美元/点 = 2
	_, tr2, err := inst.marketOrder("human", "sell", 1)
	if err != nil {
		t.Fatalf("市价卖失败：%v", err)
	}
	if math.Abs(tr2.RealizedPnl-2) > 1e-9 {
		t.Fatalf("平仓应当实现 2，实际 %v", tr2.RealizedPnl)
	}
	if inst.Position.Quantity != 1 || math.Abs(inst.RealizedPnl-2) > 1e-9 {
		t.Fatalf("平仓后持仓/盈亏不对：%+v pnl=%v", inst.Position, inst.RealizedPnl)
	}
}

func TestMarketOrderValidation(t *testing.T) {
	inst := newTradingInstance()
	cases := []struct {
		side string
		qty  float64
	}{
		{"hold", 1},
		{"buy", 0},
		{"buy", -2},
		// 手数只能是整数，和挂单同一条规则
		{"buy", 0.5},
		{"sell", 1.5},
	}
	for _, c := range cases {
		if _, _, err := inst.marketOrder("human", c.side, c.qty); err == nil {
			t.Fatalf("这些参数本该报错：%+v", c)
		}
	}
	if len(inst.Orders) != 0 || len(inst.Trades) != 0 {
		t.Fatalf("被拒的市价单不该留下任何记录：orders=%d trades=%d", len(inst.Orders), len(inst.Trades))
	}
	if inst.Position.Quantity != 0 {
		t.Fatalf("被拒的市价单不该改持仓：%+v", inst.Position)
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
		// 手数只能是整数：0.5 手、1.5 手在期货里根本报不出去
		{"buy", KindLimit, 100, 0.5},
		{"buy", KindLimit, 100, 1.5},
		{"sell", KindStop, 100, 2.25},
	}
	for _, c := range cases {
		if _, err := inst.placeOrder("human", c.side, c.kind, c.price, c.qty); err == nil {
			t.Fatalf("这些参数本该报错：%+v", c)
		}
	}
	// 整数手数照常通过，并且数量就是整数本身（3 手不是 3.0000001）
	o, err := inst.placeOrder("human", "buy", KindLimit, 99, 3)
	if err != nil {
		t.Fatalf("整数手数不该被拒：%v", err)
	}
	if o.Quantity != 3 {
		t.Fatalf("数量应当原样存成 3，实际 %v", o.Quantity)
	}
}

// TestPointValueFromTickValue 钉住「一跳值多少美元 -> 每点多少美元」的换算。
//
// 用户只在 _symbols.json 里填 tick 和 tickValue；结算乘数是两者相除得到的，
// 不另存一份，免得改了一处忘了另一处。
func TestPointValueFromTickValue(t *testing.T) {
	cases := []struct {
		symbol    string
		tick      float64
		tickValue float64
		want      float64
	}{
		{"MNQ", 0.25, 0.5, 2},
		{"MES", 0.25, 1.25, 5},
		{"MGC", 0.1, 1, 10},
		{"CL", 0.01, 10, 1000},
	}
	for _, c := range cases {
		inst := &Instance{Symbol: c.symbol, Tick: c.tick, TickValue: c.tickValue}
		if got := inst.pointValue(); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%s：每点应当值 %v 美元，实际 %v", c.symbol, c.want, got)
		}
	}
	// 没配置（或只配了一半）时退化成「每点 1 美元」，而不是 0
	if got := (&Instance{}).pointValue(); got != 1 {
		t.Fatalf("没配置时应当按每点 1 美元兜底，实际 %v", got)
	}
}

// TestTickValueSettlesPnlInUsd 确认已实现盈亏和浮动盈亏都乘上了每点价值。
// 价格差 × 手数只是「点数」，不换成美元的话 MNQ 和 CL 会显示成同一个数。
func TestTickValueSettlesPnlInUsd(t *testing.T) {
	// MNQ：0.5 美元 / 0.25 点 = 2 美元/点
	inst := newTradingInstance()
	inst.Tick, inst.TickValue = 0.25, 0.5

	inst.applyFill(Order{Side: "buy", Quantity: 2, Price: 18000}, 18000, inst.Bars[0].Time)
	tr := inst.applyFill(Order{Side: "sell", Quantity: 2, Price: 18001.5}, 18001.5, inst.Bars[0].Time)
	// 1.5 点 × 2 手 × 2 美元/点 = 6 美元
	if math.Abs(tr.RealizedPnl-6) > 1e-9 {
		t.Fatalf("MNQ 平 2 手、赚 1.5 点应当是 6 美元，实际 %v", tr.RealizedPnl)
	}

	// 浮动盈亏：100 开 2 手多，游标到收盘 102（2 点）-> 2×2×2 = 8 美元
	inst2 := newTradingInstance()
	inst2.Tick, inst2.TickValue = 0.25, 0.5
	inst2.applyFill(Order{Side: "buy", Quantity: 2, Price: 100}, 100, inst2.Bars[0].Time)
	inst2.Cursor = 2 // 收盘 102
	if got := inst2.unrealizedPnl(); math.Abs(got-8) > 1e-9 {
		t.Fatalf("MNQ 浮盈应当是 8 美元，实际 %v", got)
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

// TestMoveOrderChangesPriceOnly 钉住「拖挂单线 = 只改价」。
//
// 单型、数量、编号都不能动：拖过现价就自动换单型，会让订单在用户手里悄悄变性质
// （止损单变限价单），而他以为自己只是挪了条线。
func TestMoveOrderChangesPriceOnly(t *testing.T) {
	inst := newTradingInstance()
	o, err := inst.placeOrder("human", "buy", KindLimit, 97.5, 2)
	if err != nil {
		t.Fatalf("挂单失败：%v", err)
	}
	got, err := inst.moveOrder(o.ID, 96.25)
	if err != nil {
		t.Fatalf("改价失败：%v", err)
	}
	if got.Price != 96.25 {
		t.Fatalf("价格应当改成 96.25，实际 %v", got.Price)
	}
	if got.Kind != KindLimit || got.Quantity != 2 || got.ID != o.ID {
		t.Fatalf("改价不该动单型/数量/编号：%+v", got)
	}
	if got.Status != OrderOpen || got.PlacedIndex != 0 || got.PlacedAt != "2026-01-05 09:00" {
		t.Fatalf("改价不改下单时间与状态：%+v", got)
	}

	// 改成 96.25 之后按新价参与撮合：09:05 最低 98 不成交，09:10 最低 97 也不成交
	if _, fills, _ := inst.advance(emptyDS()); len(fills) != 0 {
		t.Fatalf("09:05 最低 98 不该成交：%v", fills)
	}
	if _, fills, _ := inst.advance(emptyDS()); len(fills) != 0 {
		t.Fatalf("09:10 最低 97 不该成交 96.25 的买单：%v", fills)
	}
}

// TestMoveOrderRejectsClosedAndBadPrice 兜住两条边界：已成交的单不能改价，
// 价格必须是大于 0 的有限数字（前端理论上会做好，但协议不能只信前端）。
func TestMoveOrderRejectsClosedAndBadPrice(t *testing.T) {
	inst := newTradingInstance()
	o, _ := inst.placeOrder("human", "buy", KindLimit, 99, 1)
	if _, err := inst.cancelOrder(o.ID); err != nil {
		t.Fatalf("撤单失败：%v", err)
	}
	if _, err := inst.moveOrder(o.ID, 98); err == nil {
		t.Fatal("已撤销的单不该能改价")
	}
	if _, err := inst.moveOrder("o999", 98); err == nil {
		t.Fatal("不存在的单应当报错")
	}

	o2, _ := inst.placeOrder("human", "buy", KindLimit, 99, 1)
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := inst.moveOrder(o2.ID, bad); err == nil {
			t.Fatalf("价格 %v 应当被拒", bad)
		}
	}
}

// fillAndRecord 走和 marketOrder / advance 完全一样的两步：applyFill 算持仓和盈亏、
// 返回成交流水，由调用方追加进 Trades。closedLots 只读 Trades，所以测试必须照这个约定来。
func fillAndRecord(inst *Instance, o Order, price float64, t string) Trade {
	tr := inst.applyFill(o, price, t)
	inst.Trades = append(inst.Trades, tr)
	return tr
}

// TestClosedLotsPerLotPnlNotAveragedShare 钉住「分批开、一次平」时每根平仓虚线各自的盈亏。
//
// 复现的是真实场景：5 手空单分批开在 5 个不同价位（7076.14 / 7081 / 7083.25 /
// 7081.5 / 7082.5），然后一张市价多单 5 手一次全平（7079.25）。
//
// 这 5 笔的开仓价各不相同，盈亏**本来就该有正有负**（第一笔开在平仓价之下，是亏的）。
// 旧实现把后端那一笔的总盈亏（平均成本法算出的 +40.70）按手数平均摊成 5 份，
// 于是 5 根虚线**全部显示 +8.14** —— 既抹掉了各批开仓价的差别，又把亏损的一笔显示成盈利。
//
// 这里钉三件事：
//  1. 5 根虚线各自的开仓价对得上原来那 5 笔（没有配错批次）；
//  2. 每根的盈亏等于**用自己开仓价算**的真实值，且五根**互不相同**、有正有负；
//  3. 五根加起来 = 后端那一笔的总盈亏（不是近似相等，是同一个数）。
func TestClosedLotsPerLotPnlNotAveragedShare(t *testing.T) {
	// MES：0.25 一跳、1.25 美元一跳 -> 每点 5 美元
	inst := &Instance{ID: "ins-lot", Symbol: "CME_MINI:MES1!", Folder: "CME_MINI_MES1!", Tick: 0.25, TickValue: 1.25}
	inst.Bars = []Bar{{Time: "2026-02-25 07:05", Open: 7079.25, High: 7080, Low: 7079, Close: 7079.25}}
	inst.Cursor = 0

	// 5 手空单分批开在 5 个价位
	opens := []float64{7076.14, 7081, 7083.25, 7081.5, 7082.5}
	for _, p := range opens {
		fillAndRecord(inst, Order{ID: "o", Side: "sell", Quantity: 1, Price: p}, p, inst.Bars[0].Time)
	}
	if inst.Position.Quantity != -5 {
		t.Fatalf("应当是 5 手空头，实际 %v", inst.Position.Quantity)
	}

	// 一次平掉 5 手
	closeTr := fillAndRecord(inst, Order{ID: "oc", Side: "buy", Quantity: 5, Price: 7079.25}, 7079.25, inst.Bars[0].Time)
	if inst.Position.Quantity != 0 {
		t.Fatalf("应当回到空仓，实际 %v", inst.Position.Quantity)
	}

	lots := inst.closedLots()
	if len(lots) != 5 {
		t.Fatalf("5 笔分批开的空仓应当配出 5 根平仓虚线，实际 %d 根", len(lots))
	}

	// 每点 5 美元；空头的盈亏是 (平仓价 - 开仓价) × 手数 × (-1) × 每点价值
	want := []float64{-15.55, 8.75, 20.00, 11.25, 16.25}
	seen := map[float64]bool{}
	sum := 0.0
	for i, c := range lots {
		if c.Side != "short" {
			t.Fatalf("第 %d 根应当是平掉的空头，实际 side=%q", i, c.Side)
		}
		if c.OpenPrice != opens[i] {
			t.Fatalf("第 %d 根配错批次：开仓价应当是 %v，实际 %v", i, opens[i], c.OpenPrice)
		}
		if c.ClosePrice != 7079.25 || c.Quantity != 1 {
			t.Fatalf("第 %d 根的平仓价/手数不对：%+v", i, c)
		}
		if math.Abs(c.RealizedPnl-want[i]) > 1e-6 {
			t.Fatalf("第 %d 根盈亏应当是 %v，实际 %v", i, want[i], c.RealizedPnl)
		}
		// 关键回归点：五根不能是同一个数（摊平成本法的旧 bug）
		if seen[c.RealizedPnl] {
			t.Fatalf("第 %d 根的盈亏和前面的某根重复了（%v）—— 说明还是在按总量摊", i, c.RealizedPnl)
		}
		seen[c.RealizedPnl] = true
		sum += c.RealizedPnl
	}

	// 有正有负：第一笔开在平仓价之下，必须是亏的
	if lots[0].RealizedPnl >= 0 {
		t.Fatalf("开在 %v、平在 %v 的空头必须是亏损，实际 %v", opens[0], 7079.25, lots[0].RealizedPnl)
	}
	// 逐批之和 = 后端那一笔的总盈亏
	if math.Abs(sum-closeTr.RealizedPnl) > 1e-6 {
		t.Fatalf("逐批之和 %v 应当等于该笔总盈亏 %v", sum, closeTr.RealizedPnl)
	}
	if math.Abs(sum-40.70) > 1e-6 {
		t.Fatalf("这个场景的总盈亏应当是 40.70，实际 %v", sum)
	}
}

// TestClosedLotsPartialCloseKeepsOwnPrice 确认**部分平仓**时也不串味：
// 平掉 2 手只该产出 2 根虚线，且盈亏按被消耗的那两批的开仓价算，
// 剩下 3 手的批次原样留着给下一次平仓。
func TestClosedLotsPartialCloseKeepsOwnPrice(t *testing.T) {
	inst := &Instance{ID: "ins-part", Symbol: testSymbol, Folder: testFolder, Tick: 0.25, TickValue: 0.5} // 每点 2 美元
	inst.Bars = []Bar{{Time: "2026-01-05 09:00", Open: 100, High: 101, Low: 99, Close: 100}}
	inst.Cursor = 0

	for _, p := range []float64{100, 102, 104} {
		fillAndRecord(inst, Order{Side: "buy", Quantity: 1, Price: p}, p, inst.Bars[0].Time)
	}
	// 平掉 2 手（只够吃掉最早的两批）
	fillAndRecord(inst, Order{Side: "sell", Quantity: 2, Price: 110}, 110, inst.Bars[0].Time)

	lots := inst.closedLots()
	if len(lots) != 2 {
		t.Fatalf("平掉 2 手只该有 2 根虚线，实际 %d", len(lots))
	}
	// 多头：(110 - 开仓价) × 1 × 2 美元/点
	if math.Abs(lots[0].RealizedPnl-20) > 1e-9 || math.Abs(lots[1].RealizedPnl-16) > 1e-9 {
		t.Fatalf("前两批的盈亏应当是 20 / 16，实际 %v / %v", lots[0].RealizedPnl, lots[1].RealizedPnl)
	}
	if lots[0].OpenPrice != 100 || lots[1].OpenPrice != 102 {
		t.Fatalf("应当配到最早的两批（100 / 102），实际 %v / %v", lots[0].OpenPrice, lots[1].OpenPrice)
	}
	// 账户口径是平均成本法：均价仍是三批的加权平均 102，**不会**变成最后一批的 104。
	// （批次 104 只存在于 closedLots 的 FIFO 队列里 —— 这正是两者的分工。）
	if inst.Position.Quantity != 1 || inst.Position.AveragePrice != 102 {
		t.Fatalf("剩下一手多头、均价应当仍是 102，实际 %+v", inst.Position)
	}

	// 把最后 1 手也平掉：第三根虚线必须接在 104 那批上，盈亏 (112-104)×2 美元/点 = 16
	fillAndRecord(inst, Order{Side: "sell", Quantity: 1, Price: 112}, 112, inst.Bars[0].Time)
	lots = inst.closedLots()
	if len(lots) != 3 {
		t.Fatalf("全部平掉后应当有 3 根虚线，实际 %d", len(lots))
	}
	if lots[2].OpenPrice != 104 || math.Abs(lots[2].RealizedPnl-16) > 1e-9 {
		t.Fatalf("第三根应当配到 104 那批、盈亏 16，实际 %+v", lots[2])
	}
	// 先平掉的两批不能因为后面又平了一次而改变盈亏
	if math.Abs(lots[0].RealizedPnl-20) > 1e-9 || math.Abs(lots[1].RealizedPnl-16) > 1e-9 {
		t.Fatalf("前两批的盈亏被后续成交改动了：%v / %v", lots[0].RealizedPnl, lots[1].RealizedPnl)
	}
}

// TestClosedLotsPureReadOnly 确认 closedLots 只是重算：不改持仓、不改成交流水、不改累计盈亏。
func TestClosedLotsPureReadOnly(t *testing.T) {
	inst := &Instance{ID: "ins-ro", Symbol: testSymbol, Folder: testFolder}
	inst.Bars = []Bar{{Time: "2026-01-05 09:00", Open: 100, High: 101, Low: 99, Close: 100}}
	inst.Cursor = 0
	fillAndRecord(inst, Order{Side: "sell", Quantity: 2, Price: 100}, 100, inst.Bars[0].Time)
	fillAndRecord(inst, Order{Side: "buy", Quantity: 2, Price: 95}, 95, inst.Bars[0].Time)

	pos, trades, pnl := inst.Position, len(inst.Trades), inst.RealizedPnl
	first := inst.closedLots()
	second := inst.closedLots()

	if len(first) != 1 {
		t.Fatalf("应当只有 1 根虚线，实际 %d", len(first))
	}
	if inst.Position != pos || len(inst.Trades) != trades || inst.RealizedPnl != pnl {
		t.Fatalf("closedLots 不该改任何状态：%+v trades=%d pnl=%v", inst.Position, len(inst.Trades), inst.RealizedPnl)
	}
	// 重算要稳定：画线、命中测试、信息卡是分开调的，结果必须一致
	if len(second) != len(first) || second[0].RealizedPnl != first[0].RealizedPnl {
		t.Fatalf("两次重算结果不一致：%+v vs %+v", first[0], second[0])
	}
}

// 同一根 K 线同时穿过止损和止盈（一根长影线两头都扎）：只成交**止损**那一张，
// 另一张按 OCO 自动撤销。
//
// 这是 5 分钟粒度绕不过去的限制：一根 K 线只有最高/最低价，看不出先摸到哪边。
// 不成组的话，止损平完仓之后止盈接着成交，会把仓位平过头、凭空开出一个反向仓 ——
// 在真实数据上跑一天能出现好几次，而且从成交流水里很难看出来。
func TestOCOGroupFillsOnlyTheFirstOrder(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	// 09:05 那根：最高 102、最低 98 —— 101 的 stop 和 99 的 limit 都会判为触发。
	// 两张都触发时判止损（保守方向），见 ocoWinner。
	stop, err := inst.placeOrderGrouped("human", "buy", KindStop, 101, 1, "g1")
	if err != nil {
		t.Fatalf("挂止损失败：%v", err)
	}
	limit, err := inst.placeOrderGrouped("human", "buy", KindLimit, 99, 1, "g1")
	if err != nil {
		t.Fatalf("挂止盈失败：%v", err)
	}

	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("同组只该成交 1 张，实际 %d 张：%+v", len(fills), fills)
	}
	if fills[0].OrderID != stop.ID {
		t.Fatalf("应当成交止损单 %s，实际 %s", stop.ID, fills[0].OrderID)
	}
	if inst.Orders[0].Status != OrderFilled {
		t.Fatalf("止损单应当成交，实际 %s", inst.Orders[0].Status)
	}
	if inst.Orders[1].Status != OrderCancelled || inst.Orders[1].ID != limit.ID {
		t.Fatalf("止盈单应当被自动撤销，实际 %s（%s）", inst.Orders[1].Status, inst.Orders[1].ID)
	}
	// 关键：仓位是 +1（只成交一张），不是 +2，也不是「平完又反手」
	if inst.Position.Quantity != 1 {
		t.Fatalf("只该成交 1 手，实际持仓 %v", inst.Position.Quantity)
	}
}

// 反过来挂：先挂止盈、后挂止损，同根双触发时**仍然判止损**。
//
// 这条是「止损优先」是否真的与挂单顺序无关的唯一证据。改回「先挂的优先」这个测试就红。
func TestOCOStopWinsEvenWhenPlacedLast(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	limit, err := inst.placeOrderGrouped("human", "buy", KindLimit, 99, 1, "g1")
	if err != nil {
		t.Fatalf("挂止盈失败：%v", err)
	}
	stop, err := inst.placeOrderGrouped("human", "buy", KindStop, 101, 1, "g1")
	if err != nil {
		t.Fatalf("挂止损失败：%v", err)
	}

	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("同组只该成交 1 张，实际 %d 张：%+v", len(fills), fills)
	}
	if fills[0].OrderID != stop.ID {
		t.Fatalf("后挂的止损也该赢，实际成交 %s", fills[0].OrderID)
	}
	if inst.Orders[0].Status != OrderCancelled || inst.Orders[0].ID != limit.ID {
		t.Fatalf("先挂的止盈单应当被撤销，实际 %s（%s）", inst.Orders[0].Status, inst.Orders[0].ID)
	}
	if inst.Position.Quantity != 1 {
		t.Fatalf("只该成交 1 手，实际持仓 %v", inst.Position.Quantity)
	}
}

// 只有止盈触发时：止盈成交，止损**当场作废**（不用等机器人下一次同步）。
//
// OCO 是「一张成交、另一张作废」，不是「两张都触发才二选一」。仓位已经平掉了，
// 止损再挂着就会在下一次被触发时开出一个没人要的反向仓。
func TestOCOCancelsSiblingWhenOnlyTargetTriggers(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	// 09:05 那根最高 102、最低 98。止损放在 105（没触发），止盈 99（触发）。
	stop, err := inst.placeOrderGrouped("human", "buy", KindStop, 105, 1, "g1")
	if err != nil {
		t.Fatalf("挂止损失败：%v", err)
	}
	limit, err := inst.placeOrderGrouped("human", "buy", KindLimit, 99, 1, "g1")
	if err != nil {
		t.Fatalf("挂止盈失败：%v", err)
	}

	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 || fills[0].OrderID != limit.ID {
		t.Fatalf("应当只成交止盈单，实际 %+v", fills)
	}
	if inst.Orders[0].Status != OrderCancelled {
		t.Fatalf("没触发的止损单也该被 OCO 撤销，实际 %s（%s）", inst.Orders[0].Status, stop.ID)
	}
}

// 不同组之间互不影响：两张单各自都能成交。
func TestOrdersInDifferentGroupsAllFill(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()
	if _, err := inst.placeOrderGrouped("human", "buy", KindStop, 101, 1, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.placeOrderGrouped("human", "buy", KindLimit, 99, 2, "g2"); err != nil {
		t.Fatal(err)
	}
	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 2 {
		t.Fatalf("不同组应当各成交一张，实际 %d 张", len(fills))
	}
	if inst.Position.Quantity != 3 {
		t.Fatalf("持仓应当是 3 手，实际 %v", inst.Position.Quantity)
	}
}

// ---------------------------------------------------------------------------
// nATR 定手数：市价开仓 + 自动在最大亏损价格挂止损
// ---------------------------------------------------------------------------

// 买入开仓：止损挂在「成交价 − 距离」，反方向、同手数，等下一根才参与撮合。
func TestMarketOrderWithStopPlacesStopBelowForBuy(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()

	// 现价 100（第一根收盘），距离 3 点 -> 止损 97
	o, tr, stop, err := inst.marketOrderWithStop("human", "buy", 2, 3)
	if err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if tr.Price != 100 || inst.Position.Quantity != 2 {
		t.Fatalf("应当按现价 100 成交 2 手：trade=%+v pos=%+v", tr, inst.Position)
	}
	if stop == nil {
		t.Fatal("带 stopDistance 的市价单必须挂一张止损")
	}
	if stop.Side != "sell" || stop.Kind != KindStop {
		t.Fatalf("多头的保护单应当是 sell/stop，实际 %s/%s", stop.Side, stop.Kind)
	}
	if stop.Price != 97 {
		t.Fatalf("止损价应当是 100-3=97，实际 %v", stop.Price)
	}
	if stop.Quantity != 2 || stop.Status != OrderOpen {
		t.Fatalf("止损应当是同手数、挂着的：%+v", *stop)
	}
	if stop.ID == o.ID {
		t.Fatal("止损单和开仓单不该是同一张")
	}

	// 09:05 最低 98 > 97：不成交（止损不能在本根立刻吃掉自己刚开的仓）
	if _, fills, err := inst.advance(ds); err != nil || len(fills) != 0 {
		t.Fatalf("09:05 不该止损：fills=%v err=%v", fills, err)
	}
	// 09:10 最低 97：触发，成交价就是 97，亏 (97-100)×2 = -6
	_, fills, err := inst.advance(ds)
	if err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("应当止损成交 1 笔，实际 %d", len(fills))
	}
	if fills[0].Price != 97 || math.Abs(fills[0].RealizedPnl+6) > 1e-9 {
		t.Fatalf("止损应当是 @97 亏 6，实际 %+v", fills[0])
	}
	if inst.Position.Quantity != 0 || math.Abs(inst.RealizedPnl+6) > 1e-9 {
		t.Fatalf("止损后应当空仓、累计 -6：pos=%+v pnl=%v", inst.Position, inst.RealizedPnl)
	}
}

// 卖出开仓：止损往上加，方向反过来。
func TestMarketOrderWithStopPlacesStopAboveForSell(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()

	_, _, stop, err := inst.marketOrderWithStop("human", "sell", 1, 3)
	if err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if stop == nil || stop.Side != "buy" || stop.Kind != KindStop || stop.Price != 103 {
		t.Fatalf("空头的保护单应当是 buy/stop @103，实际 %+v", stop)
	}
	if _, fills, _ := inst.advance(ds); len(fills) != 0 {
		t.Fatalf("09:05 最高 102 < 103，不该止损：%v", fills)
	}
	_, fills, err := inst.advance(ds) // 09:10 最高 103
	if err != nil || len(fills) != 1 {
		t.Fatalf("应当止损成交：fills=%v err=%v", fills, err)
	}
	if math.Abs(fills[0].RealizedPnl+3) > 1e-9 {
		t.Fatalf("空头止损应当亏 3，实际 %v", fills[0].RealizedPnl)
	}
	if inst.Position.Quantity != 0 {
		t.Fatalf("止损后应当空仓：%+v", inst.Position)
	}
}

// stopDistance = 0 就是普通市价单：一张保护单都不该冒出来（老客户端不带这个字段）。
func TestMarketOrderWithoutStopDistancePlacesNothing(t *testing.T) {
	inst := newTradingInstance()
	_, _, stop, err := inst.marketOrderWithStop("human", "buy", 1, 0)
	if err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if stop != nil {
		t.Fatalf("距离为 0 时不该挂止损，实际 %+v", *stop)
	}
	if len(inst.Orders) != 1 || inst.Orders[0].Status != OrderFilled {
		t.Fatalf("应当只有那一张市价单：%+v", inst.Orders)
	}
}

// 距离不合法时**一个仓位都不该留下**：先把止损价验清楚再开仓。
func TestMarketOrderWithStopRejectsBadDistanceBeforeEntry(t *testing.T) {
	cases := []struct {
		name     string
		distance float64
	}{
		{"负数", -1},
		{"NaN", math.NaN()},
		{"大到算出负价", 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inst := newTradingInstance()
			if _, _, _, err := inst.marketOrderWithStop("human", "buy", 1, c.distance); err == nil {
				t.Fatal("不合法的止损距离应当报错")
			}
			if inst.Position.Quantity != 0 || len(inst.Orders) != 0 || len(inst.Trades) != 0 {
				t.Fatalf("校验失败时不该留下任何仓位或订单：pos=%+v orders=%d trades=%d",
					inst.Position, len(inst.Orders), len(inst.Trades))
			}
		})
	}
}
