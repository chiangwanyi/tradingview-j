package replay

// 手续费是全局的：自由模式和考核账户都按同一张费率表扣，扣在手成交那一刻（applyFill），
// 扣完的结果直接写进 Trade.RealizedPnl —— 所以下游（实例已实现盈亏、交易记录统计、
// 界面上的已实现盈亏）拿到的天然是净额，不存在「毛额/净额」两本账。

import (
	"math"
	"testing"

	"tradingview-j/internal/store"
)

// TestCommissionBySymbol 钉住费率表本身：MES/MNQ 每手每边 0.5、MGC 0.8，认不出来不扣。
// 认不出来返回 0 是刻意的 —— 宁可显示「没有这笔费用」，也不猜一个数去改用户的盈亏。
func TestCommissionBySymbol(t *testing.T) {
	cases := []struct {
		symbol string
		want   float64
		why    string
	}{
		{"CME_MINI:MES1!", 0.5, "MES 每手每边 0.5"},
		{"CME_MINI:MNQ1!", 0.5, "MNQ 每手每边 0.5"},
		{"COMEX_MINI:MGC1!", 0.8, "MGC 每手每边 0.8"},
		{"TEST:ABC", 0, "没登记的标的不扣"},
		{"NYSE:XYZ", 0, "认不出来的标的不扣"},
	}
	for _, c := range cases {
		if got := store.DefaultCommission(c.symbol); got != c.want {
			t.Fatalf("%s：%s，期望 %v 实际 %v", c.symbol, c.why, c.want, got)
		}
	}
}

// TestCommissionEntryAndExitBothCharged 钉住「开平各收一次」：
// 开 2 手收 1.0、平 2 手再收 1.0，两笔合起来净 0（毛盈亏 2.0 - 手续费 2.0）。
func TestCommissionEntryAndExitBothCharged(t *testing.T) {
	inst := newTradingInstance()
	inst.CommissionPerContract = 0.5
	ds := emptyDS()

	_, entry, err := inst.marketOrder("human", "buy", 2) // 收盘 100
	if err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if entry.Commission != 1.0 {
		t.Fatalf("开仓 2 手应收 1.0 手续费，实际 %v", entry.Commission)
	}
	// 开仓没有价差，只有成本：所以它是负的。这不是 bug，是成交本身的花费。
	if entry.RealizedPnl != -1.0 {
		t.Fatalf("开仓成交的净额应当是 -1.0（只有手续费），实际 %v", entry.RealizedPnl)
	}

	if _, _, err := inst.advance(ds); err != nil { // 09:05 收盘 101
		t.Fatalf("步进失败：%v", err)
	}
	_, exit, err := inst.marketOrder("human", "sell", 2)
	if err != nil {
		t.Fatalf("平仓失败：%v", err)
	}
	// 毛盈亏 (101-100)*2*1 = 2，手续费 1.0，净 1.0
	if exit.RealizedPnl != 1.0 {
		t.Fatalf("平仓净额应当是 1.0，实际 %v", exit.RealizedPnl)
	}
	if math.Abs(inst.RealizedPnl) > 1e-9 {
		t.Fatalf("一进一出毛赚 2、手续费 2，净额应当是 0，实际 %v", inst.RealizedPnl)
	}
	// 不变量：实例的已实现盈亏 = 成交流水净额之和（下游都读这一个数）
	sum := 0.0
	for _, tr := range inst.Trades {
		sum += tr.RealizedPnl
	}
	if math.Abs(sum-inst.RealizedPnl) > 1e-9 {
		t.Fatalf("成交流水之和 %v 与实例已实现盈亏 %v 不符", sum, inst.RealizedPnl)
	}
}

// TestCommissionReversalChargedOnce 钉住「反手一笔按成交手数收一次」：
// 一口气平 3 开 2，就按 5 手 × 0.5 = 2.5 收，不拆成「平 3 收一次、开 2 再收一次」。
func TestCommissionReversalChargedOnce(t *testing.T) {
	inst := newTradingInstance()
	inst.CommissionPerContract = 0.5
	ds := emptyDS()

	if _, _, err := inst.marketOrder("human", "buy", 3); err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if _, _, err := inst.advance(ds); err != nil { // 收盘 101
		t.Fatalf("步进失败：%v", err)
	}
	_, tr, err := inst.marketOrder("human", "sell", 5)
	if err != nil {
		t.Fatalf("反手失败：%v", err)
	}
	if tr.Commission != 2.5 {
		t.Fatalf("反手 5 手应当只收 2.5，实际 %v", tr.Commission)
	}
	// 毛盈亏 (101-100)*3 = 3，净 3 - 2.5 = 0.5
	if tr.RealizedPnl != 0.5 {
		t.Fatalf("反手净额应当是 0.5，实际 %v", tr.RealizedPnl)
	}
	if inst.Position.Quantity != -2 {
		t.Fatalf("反手后应当是 -2 手，实际 %v", inst.Position.Quantity)
	}
}

// TestCommissionZeroRateUnchanged 钉住「费率为 0 时一个数字都不变」：
// 测试标的 TEST:ABC 和所有没登记的标的走这条路，所以既有行为不受这次改动影响。
func TestCommissionZeroRateUnchanged(t *testing.T) {
	inst := newTradingInstance()
	ds := emptyDS()

	if _, _, err := inst.marketOrder("human", "buy", 2); err != nil {
		t.Fatalf("开仓失败：%v", err)
	}
	if inst.RealizedPnl != 0 {
		t.Fatalf("费率为 0 时开仓不该有任何盈亏，实际 %v", inst.RealizedPnl)
	}
	if _, _, err := inst.advance(ds); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	_, tr, err := inst.marketOrder("human", "sell", 2)
	if err != nil {
		t.Fatalf("平仓失败：%v", err)
	}
	if tr.Commission != 0 || tr.RealizedPnl != 2.0 {
		t.Fatalf("费率为 0 时应当净赚 2.0 且零手续费，实际 comm=%v pnl=%v", tr.Commission, tr.RealizedPnl)
	}
}

// TestCommissionSettlesOnStopFill 钉住挂单成交（不是市价单）这条路径也扣费：
// 手续费只有 applyFill 一个扣点，止损被触发时不写在这里就会漏。
func TestCommissionSettlesOnStopFill(t *testing.T) {
	inst := newTradingInstance()
	inst.CommissionPerContract = 0.5
	ds := emptyDS()

	if _, _, err := inst.marketOrder("human", "buy", 4); err != nil { // 100，收 2.0
		t.Fatalf("开仓失败：%v", err)
	}
	// 09:05 最低 98：止损 98 会被触发
	if _, err := inst.placeOrder("human", "sell", KindStop, 98, 4); err != nil {
		t.Fatalf("挂止损失败：%v", err)
	}
	_, fills, err := inst.advance(ds)
	if err != nil || len(fills) != 1 {
		t.Fatalf("止损应当成交一笔：fills=%d err=%v", len(fills), err)
	}
	if fills[0].Commission != 2.0 {
		t.Fatalf("4 手止损应当收 2.0，实际 %v", fills[0].Commission)
	}
	// 毛盈亏 (98-100)*4 = -8，扣掉两次手续费 4.0 → -12
	if fills[0].RealizedPnl != -10.0 {
		t.Fatalf("止损这笔净额应当是 -10.0（-8 价差 - 2 手续费），实际 %v", fills[0].RealizedPnl)
	}
	if inst.RealizedPnl != -12.0 {
		t.Fatalf("累计净额应当是 -12.0（含开仓那 2.0），实际 %v", inst.RealizedPnl)
	}
}

// TestCommissionPersistedInMeta 钉住「费率随实例冻结」：落盘再读回来还是那个值，
// 改费率表不会给已建的实例换一套成本。
func TestCommissionPersistedInMeta(t *testing.T) {
	inst := newTradingInstance()
	inst.CommissionPerContract = 0.8

	f := inst.toFiles()
	if f.Meta.CommissionPerContract != 0.8 {
		t.Fatalf("meta 里没带上费率：%v", f.Meta.CommissionPerContract)
	}
	if back := f.toInstance(); back.CommissionPerContract != 0.8 {
		t.Fatalf("读回来费率不对：%v", back.CommissionPerContract)
	}
}
