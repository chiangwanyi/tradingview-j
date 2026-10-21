package strategy

// Caveman 的形态识别测试。全部用手工造的 K 线，不读数据目录 ——
// 这里要验的是「什么样的一对 K 线才算信号」，不是「历史上赚不赚钱」。

import (
	"math"
	"testing"
)

// bar 造一根 K 线，时间只写 "2026-10-20 10:00" 这种。
func bar(t string, o, h, l, c float64) Bar {
	return Bar{Time: t, Open: o, High: h, Low: l, Close: c, Volume: 100}
}

// flat 是一根没有方向的基数棒：实体为 0，绝不会被认成强趋势棒。
func flat(t string) Bar { return bar(t, 100, 100.5, 99.5, 100) }

// feed 逐根喂给策略，返回这串 K 线上出现的所有信号。
//
// 每喂一根就调一次 OnBar，和机器人推进的节奏一致；Position 一直是空仓
// （策略本来就只在空仓时出信号，持仓管理是执行器的事）。
func feed(t *testing.T, s Strategy, bars []Bar, tick float64) []Signal {
	t.Helper()
	var out []Signal
	for i := range bars {
		ctx := &Context{Bars: bars[:i+1], Cursor: i, Tick: tick}
		out = append(out, s.OnBar(ctx)...)
	}
	return out
}

func newCaveman(t *testing.T, overrides Values) Strategy {
	t.Helper()
	s, err := New("caveman", overrides)
	if err != nil {
		t.Fatalf("创建 caveman 失败：%v", err)
	}
	return s
}

// 强阳棒被阴棒吞回起点：做空。止损在信号棒高点外一个 tick，目标 1R。
func TestCavemanSellsFailedBullPush(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),    // 强阳：实体 1.8/2.2，收在 91% 位置
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6), // 阴棒收在 99.6，低于信号棒开盘 100
	}
	sigs := feed(t, newCaveman(t, nil), bars, 0.25)
	if len(sigs) != 1 {
		t.Fatalf("应当出现 1 个信号，实际 %d 个：%+v", len(sigs), sigs)
	}
	sig := sigs[0]
	if sig.Side != "sell" {
		t.Fatalf("方向应当是 sell，实际 %q", sig.Side)
	}
	// 止损 = 信号棒高点 102 + 1 tick(0.25) = 102.25
	if math.Abs(sig.Stop-102.25) > 1e-9 {
		t.Fatalf("止损应当在信号棒高点外一格 102.25，实际 %v", sig.Stop)
	}
	// 入场 = 触发棒收盘 99.6，R = 2.65，目标 = 99.6 - 2.65 = 96.95；
	// 向下对齐到 0.25 的格子 = 96.75（宁可少赚一格，也不假装刚好碰到）
	if math.Abs(sig.Target-96.75) > 1e-9 {
		t.Fatalf("目标应当是 1R=96.75，实际 %v", sig.Target)
	}
	if sig.Stop <= 99.6 || sig.Target >= 99.6 {
		t.Fatalf("空单的止损必须在入场之上、目标在入场之下：%+v", sig)
	}
}

// 强阴棒被阳棒吞回起点：做多。镜像验证一遍，防止方向写反。
func TestCavemanBuysFailedBearPush(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 100.2, 98, 98.2), // 强阴：实体 1.8/2.2，收在 9% 位置
		bar("2026-10-20 10:10", 98.2, 100.2, 98.1, 100.1),
	}
	sigs := feed(t, newCaveman(t, nil), bars, 0.25)
	if len(sigs) != 1 {
		t.Fatalf("应当出现 1 个信号，实际 %d 个：%+v", len(sigs), sigs)
	}
	sig := sigs[0]
	if sig.Side != "buy" {
		t.Fatalf("方向应当是 buy，实际 %q", sig.Side)
	}
	if math.Abs(sig.Stop-97.75) > 1e-9 {
		t.Fatalf("止损应当在信号棒低点外一格 97.75，实际 %v", sig.Stop)
	}
	// 入场 100.1，R = 2.35，目标 102.45 对齐到 0.25 的格子 = 102.5
	if math.Abs(sig.Target-102.5) > 1e-9 {
		t.Fatalf("目标应当是 102.5，实际 %v", sig.Target)
	}
}

// 信号棒不够强（实体只占三成）→ 不该有信号。
func TestCavemanIgnoresWeakSignalBar(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		// 振幅 2，实体 0.6 = 30%，低于默认 50%
		bar("2026-10-20 10:05", 100, 101, 99, 100.6),
		bar("2026-10-20 10:10", 100.6, 100.7, 99.4, 99.5),
	}
	if sigs := feed(t, newCaveman(t, nil), bars, 0.25); len(sigs) != 0 {
		t.Fatalf("弱信号棒不该出信号，实际 %+v", sigs)
	}
}

// 反向棒没有吞回信号棒起点（只回吐了一部分）→ 不给信号。
func TestCavemanNeedsFullEngulf(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		// 收盘 100.4 高于信号棒开盘 100：只回吐一半，不算吞没
		bar("2026-10-20 10:10", 101.8, 101.9, 100.3, 100.4),
	}
	if sigs := feed(t, newCaveman(t, nil), bars, 0.25); len(sigs) != 0 {
		t.Fatalf("没有完全吞没不该出信号，实际 %+v", sigs)
	}
}

// 同一天出现两次形态，但每日上限设成 1：只做第一单。
func TestCavemanCapsTradesPerDay(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
		bar("2026-10-20 10:15", 99.6, 101.6, 99.4, 101.4),
		bar("2026-10-20 10:20", 101.4, 101.5, 99.1, 99.2),
	}
	sigs := feed(t, newCaveman(t, Values{"maxTradesPerDay": 1}), bars, 0.25)
	if len(sigs) != 1 {
		t.Fatalf("每日上限 1 时应当只有 1 个信号，实际 %d 个", len(sigs))
	}
}

// 每日上限按**交易日**清零：次日同样的形态还能再做一单。
func TestCavemanResetsDailyCounter(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
		flat("2026-10-21 10:00"),
		bar("2026-10-21 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-21 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	sigs := feed(t, newCaveman(t, Values{"maxTradesPerDay": 1}), bars, 0.25)
	if len(sigs) != 2 {
		t.Fatalf("换交易日后计数应当清零，两天各 1 单，实际 %d 个", len(sigs))
	}
}

// 04:50 之后进入收盘收尾：形态再漂亮也不开新仓。
func TestCavemanSkipsSessionEnd(t *testing.T) {
	bars := []Bar{
		flat("2026-10-21 04:45"),
		bar("2026-10-21 04:50", 100, 102, 99.8, 101.8),
		bar("2026-10-21 04:55", 101.8, 101.9, 99.5, 99.6),
	}
	if sigs := feed(t, newCaveman(t, nil), bars, 0.25); len(sigs) != 0 {
		t.Fatalf("收盘收尾时段不该开新仓，实际 %+v", sigs)
	}
}

// 已有持仓时不叠仓、不反手：保护单归执行器管。
func TestCavemanDoesNotStackPosition(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	s := newCaveman(t, nil)
	var sigs []Signal
	for i := range bars {
		ctx := &Context{
			Bars:     bars[:i+1],
			Cursor:   i,
			Tick:     0.25,
			Position: Position{Quantity: 1, AveragePrice: 100},
		}
		sigs = append(sigs, s.OnBar(ctx)...)
	}
	if len(sigs) != 0 {
		t.Fatalf("持仓中不该再出信号，实际 %+v", sigs)
	}
}

// 风险太小（不足 4 tick）时放弃：形态再漂亮也不够付成本。
func TestCavemanSkipsTinyRisk(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		// 信号棒振幅很小：高点 100.2，触发棒收 99.7，R 只有 0.75 点 = 3 tick
		bar("2026-10-20 10:05", 99.8, 100.2, 99.7, 100.1),
		bar("2026-10-20 10:10", 100.1, 100.15, 99.6, 99.7),
	}
	if sigs := feed(t, newCaveman(t, Values{"minRTicks": 4}), bars, 0.25); len(sigs) != 0 {
		t.Fatalf("风险不足 4 tick 不该出信号，实际 %+v", sigs)
	}
	// 放宽到 2 tick 就该做这一单 —— 证明上面确实是被风险门槛挡掉的
	if sigs := feed(t, newCaveman(t, Values{"minRTicks": 0}), bars, 0.25); len(sigs) != 1 {
		t.Fatalf("门槛放宽后应当出信号，实际 %+v", sigs)
	}
}

// 只做多 / 只做空的开关。
func TestCavemanSideSwitches(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	if sigs := feed(t, newCaveman(t, Values{"allowShort": 0}), bars, 0.25); len(sigs) != 0 {
		t.Fatalf("关掉做空后不该出空单，实际 %+v", sigs)
	}
}

// 参数覆盖生效：目标改成 2R，目标价应当更远。
func TestCavemanTargetR(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	sigs := feed(t, newCaveman(t, Values{"targetR": 2}), bars, 0.25)
	if len(sigs) != 1 {
		t.Fatalf("应当有 1 个信号，实际 %d", len(sigs))
	}
	// R = 2.65，2R = 5.3，99.6 - 5.3 = 94.3，对齐 0.25 的格子 = 94.25
	if math.Abs(sigs[0].Target-94.25) > 1e-9 {
		t.Fatalf("2R 目标应当是 94.25，实际 %v", sigs[0].Target)
	}
}

// 同一根 K 线被重复喂进来只处理一次（执行器可能因为重试多次调用）。
func TestCavemanDeduplicatesSameBar(t *testing.T) {
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	s := newCaveman(t, Values{"maxTradesPerDay": 0})
	var n int
	for i := range bars {
		ctx := &Context{Bars: bars[:i+1], Cursor: i, Tick: 0.25}
		n += len(s.OnBar(ctx))
		n += len(s.OnBar(ctx)) // 同一根再调一次
	}
	if n != 1 {
		t.Fatalf("同一根重复调用应当只出一次信号，实际 %d 次", n)
	}
}
